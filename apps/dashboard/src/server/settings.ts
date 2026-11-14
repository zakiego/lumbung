// Every dashboard setting, its default and what it does. config.ts reads its
// defaults from here, and scripts/docgen.ts writes the Dashboard
// configuration page from it, so the docs can't drift from the code.
// No imports: the docs generator runs this file with plain Node.

export interface Setting {
  name: string
  default: string
  description: string
}

export const SETTINGS = [
  {
    name: "LUMBUNG_DASHBOARD_URL",
    default: "http://localhost:4871",
    description: "The address people open, without `/app`. Sign-in cookies, passkeys and the origin check bind to it, so set it to the public address behind a proxy.",
  },
  {
    name: "LUMBUNG_DASHBOARD_EXTRA_ORIGINS",
    default: "",
    description:
      "Comma-separated origins the dashboard also accepts sign-ins from, like `http://127.0.0.1:4806,http://localhost:4806` to reach it on the server when the public address is down. A request is matched to an origin by its `Host` header, and each origin gets its own sign-in cookie and origin check. Password sign-in works on them. Passkeys only work on `LUMBUNG_DASHBOARD_URL`.",
  },
  {
    name: "LUMBUNG_ENGINE_URL",
    default: "http://127.0.0.1:4870",
    description: "Where the dashboard reaches the engine's REST API. Under Docker Compose, `http://engine:4870`.",
  },
  {
    name: "LUMBUNG_PUBLIC_ENGINE_URL",
    default: "",
    description: "The engine address shown in connect snippets. When unset, the dashboard's own address, which fits a proxy that serves both under one name.",
  },
  {
    name: "LUMBUNG_SERVICE_TOKEN",
    default: "",
    description: "The engine's service token. When unset, it's read from `LUMBUNG_SERVICE_TOKEN_FILE`.",
  },
  {
    name: "LUMBUNG_SERVICE_TOKEN_FILE",
    default: "../engine/data/secrets/service-token",
    description: "A file holding the service token, like the `secrets/service-token` the engine writes in its data folder. Read on first use.",
  },
  {
    name: "LUMBUNG_DASHBOARD_DATA_DIR",
    default: "./data",
    description: "Folder for `dashboard.db` (users, sessions, passkeys and invites) and the generated session secret.",
  },
  {
    name: "LUMBUNG_AUTH_SECRET",
    default: "",
    description: "Signs sessions, at least 32 characters. When unset, one is generated on first start and kept in `secrets/auth-secret` in the data folder, mode 600.",
  },
  {
    name: "LUMBUNG_CLIENT_IP_HEADER",
    default: "",
    description: "The header that carries the client address behind a proxy, like `X-Forwarded-For` or `CF-Connecting-IP`. When unset, the connection's address is used. Same as the engine's setting.",
  },
  {
    name: "LUMBUNG_TRUSTED_PROXIES",
    default: "127.0.0.1/32,::1/128",
    description: "Comma-separated IPs or CIDR networks allowed to set the client address header. An `X-Forwarded-For` list is read from the right, skipping these. Same as the engine's setting.",
  },
] as const satisfies readonly Setting[]

export type SettingName = (typeof SETTINGS)[number]["name"]

export function settingDefault(name: SettingName): string {
  return SETTINGS.find((s) => s.name === name)?.default ?? ""
}
