package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/reyansh7/Forge/internal/runtime"
	"github.com/reyansh7/Forge/internal/store"
)

// BuildCacheIndex is the worker's image index. Nil means reuse is off,
// which is how unit tests that only stub Builder keep the old path.
// Postgres implements it. The API does not accept a cache key from a client.
type BuildCacheIndex interface {
	LookupBuildCache(ctx context.Context, applicationID, cacheKey string) (imageName, imageID string, ok bool, err error)
	RememberBuildCache(ctx context.Context, applicationID, cacheKey, imageName, imageID string) error
	ForgetBuildCache(ctx context.Context, applicationID, cacheKey string) error
}

// imageReuse is optional. HostDocker implements it. A Builder that cannot
// inspect an image ID cannot prove a hit, so the worker builds instead.
type imageReuse interface {
	ImageID(ctx context.Context, image string) (string, error)
	Tag(ctx context.Context, source, dest string) error
}

func (h Handler) buildWithCache(ctx context.Context, app store.Application, buildDir, image, kind string, env []runtime.EnvPair) (string, error, string) {
	if h.BuildCache == nil {
		log, err := h.buildOnce(ctx, app, buildDir, image, kind, env)
		return log, err, "bypass"
	}
	// Stage before hashing so the generated Dockerfile and the public
	// env file are part of the key. A hash of the git checkout alone
	// would miss both, and a later build would reuse the wrong image.
	if err := runtime.StageBuildInputs(buildDir, kind, env); err != nil {
		return "", err, "miss"
	}
	key, err := runtime.BuildCacheKey(buildDir, env)
	if err != nil {
		h.log().Error("build cache key failed", "err", err)
		log, berr := h.buildOnce(ctx, app, buildDir, image, kind, env)
		return log, berr, "miss"
	}
	short := key
	if len(short) > 12 {
		short = short[:12]
	}
	if name, id, ok, lerr := h.BuildCache.LookupBuildCache(ctx, app.ID, key); lerr != nil {
		h.log().Error("build cache lookup failed", "err", lerr)
	} else if ok {
		if reuseCachedImage(ctx, h.Builder, name, id, image) {
			return fmt.Sprintf("build cache hit key=%s reused=%s\nDocker build skipped. Health checks still run before traffic switches.\n", short, name), nil, "hit"
		}
		// The index pointed at an image that is gone or was replaced.
		// Drop the row and build. Forget does not delete the image.
		if ferr := h.BuildCache.ForgetBuildCache(ctx, app.ID, key); ferr != nil {
			h.log().Error("build cache forget failed", "err", ferr)
		}
	}
	log, err := h.buildOnce(ctx, app, buildDir, image, kind, env)
	prefix := "build cache miss key=" + short + "\n"
	if err != nil {
		return prefix + log, err, "miss"
	}
	if id, ok := inspectedImageID(ctx, h.Builder, image); ok {
		if rerr := h.BuildCache.RememberBuildCache(ctx, app.ID, key, image, id); rerr != nil {
			h.log().Error("build cache remember failed", "err", rerr)
		}
	}
	return prefix + log, nil, "miss"
}

func (h Handler) buildOnce(ctx context.Context, app store.Application, buildDir, image, kind string, env []runtime.EnvPair) (string, error) {
	log, err := h.Builder.Build(ctx, buildDir, image, kind, env)
	if err != nil {
		if pkg, ok := RepairGoPackage(log + "\n" + err.Error()); ok && strings.TrimSpace(app.BuildPackage) == "" {
			if werr := os.WriteFile(filepath.Join(buildDir, ".forge-build-package"), []byte(pkg+"\n"), 0o600); werr == nil {
				if g, ok := h.Store.(sagaStore); ok {
					_ = g.SetBuildPackage(ctx, app.ID, pkg)
				}
				app.BuildPackage = pkg
				repaired, rerr := h.Builder.Build(ctx, buildDir, image, kind, env)
				log += "\nrepair: set build_package " + pkg + " and rebuilt\n" + repaired
				err = rerr
			}
		}
	}
	return log, err
}

func reuseCachedImage(ctx context.Context, builder runtime.Builder, source, wantID, dest string) bool {
	images, ok := builder.(imageReuse)
	if !ok || source == "" || wantID == "" || dest == "" || source == dest {
		return false
	}
	got, err := images.ImageID(ctx, source)
	if err != nil || got == "" || got != wantID {
		return false
	}
	if err := images.Tag(ctx, source, dest); err != nil {
		return false
	}
	again, err := images.ImageID(ctx, dest)
	return err == nil && again == wantID
}

func inspectedImageID(ctx context.Context, builder runtime.Builder, image string) (string, bool) {
	images, ok := builder.(imageReuse)
	if !ok {
		return "", false
	}
	id, err := images.ImageID(ctx, image)
	if err != nil || !strings.HasPrefix(id, "sha256:") || len(id) != len("sha256:")+64 {
		return "", false
	}
	return id, true
}
