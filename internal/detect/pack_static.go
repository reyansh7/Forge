package detect

import "strconv"

// staticPack matches a root index.html with no language manifest.
//
// A folder of HTML/CSS/JS is a valid deployment. Images next to it do
// not classify the repo (those are assets). Language packs win first so
// a Vite app with index.html is still Node, not this pack.
type staticPack struct{}

func (staticPack) ID() Kind { return KindStatic }

func (staticPack) Match(idx fileIndex) bool {
	return idx.has("index.html")
}

func (staticPack) Analyze(idx fileIndex) (Result, error) {
	res := withPort(baseResult(KindStatic, "html", "index.html", "static HTML root"), DefaultPort, "pack")
	res.Runtime = RuntimeStatic
	res.Framework = "static-html"
	res.OutputDir = "."
	cfg, err := loadStartConfig(idx)
	if err != nil {
		return Result{}, err
	}
	res = applyStartPort(res, cfg)
	if res.Runtime == RuntimeServer && len(cfg.Start) > 0 {
		res.GeneratedDockerfile = staticHTMLImage(res.ListenPort()) + dockerfileCMD(cfg.Start)
		return res, nil
	}
	res.Runtime = RuntimeStatic
	res.GeneratedDockerfile = staticHTMLImage(res.ListenPort())
	res.ExtraFiles = map[string]string{nginxForgeName: nginxSPAConf(res.ListenPort())}
	return res, nil
}

func staticHTMLImage(port int) string {
	return `FROM nginx:1.27-alpine
COPY ` + nginxForgeName + ` /etc/nginx/nginx.conf
COPY . /usr/share/nginx/html
RUN mkdir -p /var/lib/nginx-tmp && chown -R nginx:nginx /var/lib/nginx-tmp
USER nginx
EXPOSE ` + strconv.Itoa(port) + `
ENTRYPOINT ["nginx"]
CMD ["-g","daemon off;"]
`
}

const nginxForgeName = "nginx.forge.conf"

// nginxSPAConf is a complete Forge-owned nginx.conf, not a snippet
// dropped into conf.d.
//
// Phase 3 containers run --cap-drop ALL. The stock nginx image's
// entrypoint chowns /var/cache/nginx and dies with "Operation not
// permitted". We replace the entrypoint and put pid/temp/logs on
// the writable tmpfs at /tmp so the master process never needs
// CAP_CHOWN. listen is every interface on the detected port.
func nginxSPAConf(port int) string {
	if port < 1 || port > 65535 {
		port = DefaultPort
	}
	p := strconv.Itoa(port)
	return `worker_processes 1;
error_log /var/lib/nginx-tmp/error.log warn;
pid /var/lib/nginx-tmp/nginx.pid;
events { worker_connections 128; }
http {
    include /etc/nginx/mime.types;
    default_type application/octet-stream;
    access_log /var/lib/nginx-tmp/access.log;
    sendfile on;
    client_body_temp_path /var/lib/nginx-tmp/client;
    proxy_temp_path /var/lib/nginx-tmp/proxy;
    fastcgi_temp_path /var/lib/nginx-tmp/fastcgi;
    uwsgi_temp_path /var/lib/nginx-tmp/uwsgi;
    scgi_temp_path /var/lib/nginx-tmp/scgi;
    server {
        listen ` + p + `;
        listen [::]:` + p + `;
        server_name _;
        root /usr/share/nginx/html;
        index index.html;
        location / {
            try_files $uri $uri/ /index.html;
        }
    }
}
`
}
