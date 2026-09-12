package detect

import (
	"fmt"
	"path/filepath"
	"strings"
)

type dotnetPack struct{}

func (dotnetPack) ID() Kind { return KindDotnet }

func (dotnetPack) Match(idx fileIndex) bool {
	return idx.hasExt(".csproj", ".sln")
}

func (dotnetPack) Analyze(idx fileIndex) (Result, error) {
	manifest := idx.firstWithExt(".csproj")
	if manifest == "" {
		manifest = idx.firstWithExt(".sln")
	}
	res := withPort(baseResult(KindDotnet, "csharp", manifest, ".NET project"), DefaultPort, "pack")
	res.Framework = dotnetFramework(idx)
	proj := idx.firstWithExt(".csproj")
	stem := "App"
	if proj != "" {
		s := strings.TrimSuffix(filepath.Base(proj), filepath.Ext(proj))
		if id := ident(s); id != "" {
			stem = id
		} else {
			return Result{}, fmt.Errorf("detect: .csproj name %q is not a safe identifier; add a Dockerfile", proj)
		}
	}
	res.GeneratedDockerfile = dotnetDockerfile(stem)
	return res, nil
}

func dotnetFramework(idx fileIndex) string {
	name := idx.firstWithExt(".csproj")
	if name == "" {
		return ""
	}
	raw, err := idx.read(name)
	if err != nil {
		return ""
	}
	s := string(raw)
	if strings.Contains(s, "Microsoft.AspNetCore") || strings.Contains(s, "Microsoft.NET.Sdk.Web") {
		return "aspnet"
	}
	return ""
}

func dotnetDockerfile(stem string) string {
	return `FROM mcr.microsoft.com/dotnet/sdk:8.0
WORKDIR /app
COPY . .
RUN dotnet publish -c Release -o /out
ENV PORT=8080
EXPOSE 8080
ENV ASPNETCORE_URLS=http://0.0.0.0:8080
CMD ["dotnet","/out/` + stem + `.dll"]
`
}
