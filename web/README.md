# YZ AI Gateway — Web

React 18 + TypeScript + Vite 5 + Ant Design 5 SPA for the YZ AI Gateway admin console and user console.
Built output (`dist/`) is embedded into the Go binary; the server serves `index.html` for unknown paths (history routing).

## Scripts

| Command          | Purpose                                   |
|------------------|-------------------------------------------|
| `pnpm dev`       | Vite dev server (proxies `/api`, `/v1`, `/health` to `http://127.0.0.1:8080`) |
| `pnpm build`     | Typecheck + production build into `dist/` |
| `pnpm preview`   | Preview the production build              |
| `pnpm typecheck` | `tsc --noEmit`                            |

## Structure

```
src/
  api/          axios client (token injection, 401 handling, error toasts) + typed API modules mirroring docs/api.md
  components/   shared UI: StatCard, Chart (lazy ECharts), Tags, FormDrawer, FilterBar, ProviderAvatar, ...
  hooks/        useTableQuery (page/filters -> antd pagination), useRange, useMediaQuery, useCountdown
  i18n/         i18next bootstrap; every src/locales/<lng>/<ns>.json is auto-registered as namespace <ns>
  locales/      zh-CN (default), zh-TW, en — one JSON per page namespace + common + auth
  layouts/      AppLayout (sidebar/header), route guards, nav definitions
  pages/        admin/* and console/* pages, auth/Login + ChangePassword
  stores/       zustand stores: auth (token/user), theme (light/dark), locale
  utils/        formatters (tokens/ms/percent/time), constants, provider styles
  types.ts      all API types
```

## Routes

| Route                     | Page              |
|---------------------------|-------------------|
| `/login`                  | Login             |
| `/change-password`        | Change password (forced when `must_change_password`) |
| `/admin/overview`         | Overview          |
| `/admin/accounts`         | Account pool      |
| `/admin/model-groups`     | Model groups      |
| `/admin/users`            | Users             |
| `/admin/user-groups`      | User groups       |
| `/admin/smart-route`      | Smart route (samples / decisions / stats) |
| `/admin/compliance`       | Compliance (words / samples / policy groups / audit logs) |
| `/admin/logs`             | Call logs         |
| `/admin/usage`            | Usage statistics  |
| `/admin/settings`         | Settings tabs     |
| `/admin/about`            | About             |
| `/console/models`         | Model gallery     |
| `/console/keys`           | API keys          |
| `/console/usage`          | My usage          |
| `/console/logs`           | My call logs      |
| `/console/group`          | My user group     |

Admin routes require `role === 'admin'`; console routes require any logged-in user. Unknown paths redirect by role.

## Conventions

- All UI strings live in locale JSON files; components never hardcode Chinese.
- Lists use `{items, total}` with `page` / `page_size`; time ranges use `range=24h|7d|30d|custom(&from&to)`.
- Secrets from settings arrive masked as `******`; the UI sends `******` back to keep them unchanged.
