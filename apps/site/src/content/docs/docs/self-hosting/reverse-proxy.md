---
title: Reverse proxy and TLS
description: Put Caddy, nginx or a tunnel in front of the engine and the dashboard, and tell them which header carries the client address.
sidebar:
  order: 2
---

The engine speaks plain HTTP and should only listen on `127.0.0.1`. A reverse proxy in front adds TLS and a public name. Tokens travel in the `Authorization` header, so never expose the engine without TLS.

## Caddy

Caddy gets and renews certificates on its own. The dashboard lives under `/app`, everything else goes to the engine:

```text
db.example.com {
	reverse_proxy /app* 127.0.0.1:4871
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

    location /app {
        proxy_pass http://127.0.0.1:4871;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }

    location / {
        proxy_pass http://127.0.0.1:4870;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }
}
```

## The client address

Failed logins are rate limited per client address, in the engine and in the dashboard. Behind a proxy, every request seems to come from the proxy, so tell both where the real address is, with the same two settings:

```sh
LUMBUNG_CLIENT_IP_HEADER=X-Forwarded-For
LUMBUNG_TRUSTED_PROXIES=127.0.0.1,::1
```

The header is only trusted on connections from `LUMBUNG_TRUSTED_PROXIES`, and an `X-Forwarded-For` list is read from the right, skipping trusted proxies, since clients can put anything at the left end. Behind Cloudflare (a tunnel or the proxy), use `LUMBUNG_CLIENT_IP_HEADER=CF-Connecting-IP`.

Under Docker Compose the proxy's connection arrives from the Docker network, not `127.0.0.1`, so list that network in `LUMBUNG_TRUSTED_PROXIES` (for the default bridge networks, `172.16.0.0/12`).

Set `LUMBUNG_DASHBOARD_URL` to the public address, like `https://db.example.com`. The dashboard rejects sign-ins and changes sent from any other origin.

## Paths

Everything the engine serves is under `/v1`, `/mcp` and `/health`, and everything the dashboard serves is under `/app`. If you route several services under one name, send those prefixes to each.
