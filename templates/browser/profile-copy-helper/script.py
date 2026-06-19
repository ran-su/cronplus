import json
import os
from pathlib import Path


def directory_size(path: Path) -> tuple[int, int]:
    files = 0
    bytes_total = 0
    for item in path.rglob("*"):
        if item.is_file():
            files += 1
            bytes_total += item.stat().st_size
    return files, bytes_total


def main() -> int:
    profile_dir = Path(os.environ["CRONPLUS_BROWSER_USER_DATA_DIR"])
    files, bytes_total = directory_size(profile_dir)
    status = "success" if profile_dir.exists() else "failure"
    print(
        "CRONPLUS_RESULT="
        + json.dumps(
            {
                "status": status,
                "summary": f"Copied profile has {files} file(s), {bytes_total} bytes.",
                "data": {"profile_dir": str(profile_dir), "files": files, "bytes": bytes_total},
            },
            separators=(",", ":"),
        )
    )
    return 0 if status == "success" else 1


if __name__ == "__main__":
    raise SystemExit(main())
