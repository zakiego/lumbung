---
title: Reverse proxy and TLS
description: Put Caddy, nginx or a tunnel in front of the engine, and tell it which header carries the client address.
sidebar:
  order: 2
---

The engine speaks plain HTTP and should only listen on `127.0.0.1`. A reverse proxy in front adds TLS and a public name. Tokens travel in the `Authorization` header, so never expose the engine without TLS.

## Caddy

Caddy gets and renews certificates on its own:

```text
db.example.com {
	reverse_proxy 127.0.0.1:4870
}
```

## nginx

```text
server {
    listen 443 ssl;
    server_name db.example.com;
    ssl_certificate     /etc/letsencrypt/live/db.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/db.example.com/privkey.pem;

    client_max_body_size 1100m;      # imports, up to LUMBUNG_MAX_IMPORT_BYTES
    proxy_read_timeout 15m;          # long imports and downloads
    proxy_request_buffering off;     # stream uploads instead of buffering them

    location / {
        proxy_pass http://127.0.0.1:4870;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }
}
```

## The client address

Failed logins are rate limited per client address. Behind a proxy, every request seems to come from the proxy, so tell the engine where the real address is:

```sh
LUMBUNG_CLIENT_IP_HEADER=X-Forwarded-For
LUMBUNG_TRUSTED_PROXIES=127.0.0.1,::1
```

The header is only trusted on connections from `LUMBUNG_TRUSTED_PROXIES`, and an `X-Forwarded-For` list is read from the right, skipping trusted proxies, since clients can put anything at the left end. Behind Cloudflare (a tunnel or the proxy), use `LUMBUNG_CLIENT_IP_HEADER=CF-Connecting-IP`.

## Paths

Everything the engine serves is under `/v1`, `/mcp` and `/health`. If you route several services under one name, send those three prefixes to the engine.
