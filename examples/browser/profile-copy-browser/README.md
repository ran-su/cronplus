# Profile-Copy Browser Example

This task copies a durable browser profile into a CronPlus-managed per-run
profile before opening visible Chromium with Playwright.

Use this pattern when a site needs browser state such as cookies, localStorage,
or extension data, but each run should mutate only a disposable copy.

## Prepare The Source Profile

Put the durable source profile files in `./profile`, or change
`runtime.browser.profile_source` to another directory. The checked-in
`profile/README.md` file only keeps the directory present so validation works
from a fresh checkout.

Do not commit real browser profile contents if they contain account cookies,
tokens, browsing history, or extension secrets.

## Run

```sh
cronplus validate .
cronplus import .
```

After import, use **Run Now** or wait for the schedule.

## Configuration

Set `PROFILE_COPY_URLS` to a comma-separated list of URLs. The default is:

```text
https://example.com
```

The script reports the resolved profile mode and source path so the run history
shows which browser profile policy was used.
