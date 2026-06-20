import json
import os
from pathlib import Path

from playwright.sync_api import sync_playwright


DEFAULT_URLS = "https://example.com"
URLS = [url.strip() for url in os.environ.get("PROFILE_COPY_URLS", DEFAULT_URLS).split(",") if url.strip()]


def emit_result(status: str, summary: str, data) -> None:
    print(
        "CRONPLUS_RESULT="
        + json.dumps(
            {"status": status, "summary": summary, "data": data},
            separators=(",", ":"),
        )
    )


def browser_args(cache_dir):
    args = ["--no-first-run", "--no-default-browser-check"]
    if cache_dir is not None:
        cache_dir.mkdir(parents=True, exist_ok=True)
        args.append(f"--disk-cache-dir={cache_dir}")
    return args


def main() -> int:
    profile_dir = Path(os.environ["CRONPLUS_BROWSER_USER_DATA_DIR"])
    downloads_dir = Path(os.environ["CRONPLUS_BROWSER_DOWNLOADS_DIR"])
    cache_value = os.environ.get("CRONPLUS_BROWSER_CACHE_DIR", "")
    cache_dir = Path(cache_value) if cache_value else None
    profile_mode = os.environ.get("CRONPLUS_BROWSER_PROFILE_MODE", "")
    profile_source = os.environ.get("CRONPLUS_BROWSER_PROFILE_SOURCE", "")

    downloads_dir.mkdir(parents=True, exist_ok=True)
    visited = []

    with sync_playwright() as playwright:
        context = playwright.chromium.launch_persistent_context(
            user_data_dir=str(profile_dir),
            headless=False,
            accept_downloads=True,
            downloads_path=str(downloads_dir),
            args=browser_args(cache_dir),
        )
        page = context.new_page()
        try:
            for url in URLS:
                page.goto(url, wait_until="domcontentloaded", timeout=45_000)
                visited.append({"url": url, "title": page.title()})
        finally:
            context.close()

    emit_result(
        "success",
        f"Visited {len(visited)} page(s) with a copied browser profile.",
        {
            "visited": visited,
            "profile_mode": profile_mode,
            "profile_source": profile_source,
            "profile_dir": str(profile_dir),
            "downloads_dir": str(downloads_dir),
            "cache_dir": str(cache_dir) if cache_dir else "",
        },
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        emit_result("failure", str(exc), {"error_type": type(exc).__name__})
        raise SystemExit(1)
