import json
import os
from pathlib import Path
from playwright.sync_api import sync_playwright


URLS = [url.strip() for url in os.environ.get("MONITOR_URLS", "https://example.com").split(",") if url.strip()]


def main() -> int:
    profile_dir = Path(os.environ["CRONPLUS_BROWSER_USER_DATA_DIR"])
    downloads_dir = Path(os.environ["CRONPLUS_BROWSER_DOWNLOADS_DIR"])
    downloads_dir.mkdir(parents=True, exist_ok=True)
    visited = []
    with sync_playwright() as playwright:
        context = playwright.chromium.launch_persistent_context(
            user_data_dir=str(profile_dir),
            headless=False,
            accept_downloads=True,
            downloads_path=str(downloads_dir),
            args=["--no-first-run", "--no-default-browser-check"],
        )
        page = context.new_page()
        try:
            for url in URLS:
                page.goto(url, wait_until="domcontentloaded", timeout=45_000)
                visited.append({"url": url, "title": page.title()})
        finally:
            context.close()
    print(
        "CRONPLUS_RESULT="
        + json.dumps(
            {
                "status": "success",
                "summary": f"Visited {len(visited)} page(s).",
                "data": {"visited": visited, "downloads_dir": str(downloads_dir)},
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
