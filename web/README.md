# infra-monitor web UI

Vue 3 + TypeScript + Vite. Headless primitives from [reka-ui](https://reka-ui.com), TanStack Query for data,
TanStack Table for the incident list, plain CSS with our own design tokens (`src/styles/tokens.css`, dark by default).
The build output (`dist/`) is embedded into the Go binary (`web/embed.go`).

```sh
npm ci
npm run dev        # http://localhost:5173, proxies /api to http://localhost:8080 (override with INFRA_MONITOR_URL)
npm run dev:mock   # same UI against in-memory fixtures (no backend needed); dev only, never bundled
npm run build      # type-check + production build into dist/
npm run gen:api    # regenerate src/api/schema.d.ts from ./openapi.json (`infra-monitor openapi > web/openapi.json`)
```

Layout:

- `src/api/` — `types.ts` (mirrors docs/api.md), `client.ts` (fetch + CSRF + 401 redirect + RFC 7807 errors),
  `sse.ts` (EventSource with reconnect; `timeline` events invalidate queries, `progress` feeds the agent panel), `keys.ts`.
- `src/views/` — pages (incidents list + drawer, incident page, mutes, status, profile, admin, login).
- `src/components/incident/` — incident detail tabs (overview, timeline, signals, actions, chat).
- `src/components/ui/` — small styled wrappers around reka-ui primitives.
- `src/mock/` — dev-only fixtures, loaded behind `import.meta.env.DEV && VITE_MOCK`.

Keyboard: `j`/`k` move, `Enter` opens, `Esc` closes the drawer, `/` focuses search.
Search accepts `status:`, `severity:`, `verdict:` (comma lists), `cluster:`, `namespace:`, `muted:` plus free text.
