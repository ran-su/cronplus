import json
import os
from playwright.sync_api import sync_playwright


DEBUG_PORT = int(os.environ.get("BROWSER_DEBUG_PORT", "9223"))
URLS = [url.strip() for url in os.environ.get("MONITOR_URLS", "https://example.com").split(",") if url.strip()]


def main() -> int:
    visited = []
    with sync_playwright() as playwright:
        browser = playwright.chromium.connect_over_cdp(f"http://127.0.0.1:{DEBUG_PORT}")
        context = browser.contexts[0] if browser.contexts else browser.new_context()
        page = context.new_page()
        try:
            for url in URLS:
                page.goto(url, wait_until="domcontentloaded", timeout=45_000)
                visited.append({"url": url, "title": page.title()})
        finally:
            page.close()
            # Do not close the browser object; the browser-manager task owns it.
    print(
        "CRONPLUS_RESULT="
        + json.dumps(
            {
                "status": "success",
                "summary": f"Visited {len(visited)} page(s).",
                "data": {"visited": visited},
            },
            separators=(",", ":"),
        )
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(
            "CRONPLUS_RESULT="
            + json.dumps(
                {
                    "status": "failure",
                    "summary": str(exc),
                    "data": {"error_type": type(exc).__name__},
                },
                separators=(",", ":"),
            )
        )
        raise SystemExit(1)
