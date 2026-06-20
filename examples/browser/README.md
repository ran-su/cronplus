# Browser Automation Examples

These packages are copy-ready CronPlus browser automation examples. Each directory
contains one `.cronplus.yaml` manifest, a script, and any local files needed for
`cronplus validate`.

Use them as starting points:

```sh
cronplus validate examples/browser/one-shot-browser
cronplus import examples/browser/one-shot-browser
```

The examples use CronPlus-managed browser paths instead of hard-coded temporary
directories:

- `CRONPLUS_BROWSER_USER_DATA_DIR`
- `CRONPLUS_BROWSER_DOWNLOADS_DIR`
- `CRONPLUS_BROWSER_CACHE_DIR`
- `CRONPLUS_BROWSER_PROFILE_MODE`
- `CRONPLUS_BROWSER_PROFILE_SOURCE`

## Examples

- `one-shot-browser`: opens visible Chromium with an isolated per-run profile,
  visits one or more URLs, and lets CronPlus clean up successful runs.
- `profile-copy-browser`: copies a durable profile into an isolated per-run
  profile before opening visible Chromium. This is useful when a site needs
  cookies, localStorage, or extension state, but the task should not mutate the
  durable source profile.

The scripts default to public example URLs. Override them with environment
variables in the task manifest or task package `.env` file.
