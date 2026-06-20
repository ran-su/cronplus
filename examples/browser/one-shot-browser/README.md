# One-Shot Browser Example

This task opens a visible Playwright Chromium session with a CronPlus-managed
isolated profile, visits the configured URLs, reports a structured result, and
then closes the browser.

Use this pattern when the task does not need durable browser state.

## Run

```sh
cronplus validate .
cronplus import .
```

After import, use **Run Now** or wait for the schedule.

## Configuration

Set `MONITOR_URLS` to a comma-separated list of URLs. The default is:

```text
https://example.com,https://www.iana.org/domains/reserved
```

The script uses:

- `CRONPLUS_BROWSER_USER_DATA_DIR` for the browser profile.
- `CRONPLUS_BROWSER_DOWNLOADS_DIR` for downloads.
- `CRONPLUS_BROWSER_CACHE_DIR` for Chromium disk cache when isolated cache is
  enabled.

The manifest keeps failed run directories for debugging and deletes successful
run directories after the browser exits.
