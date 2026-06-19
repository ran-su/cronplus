import json
import os
import signal
import socket
import subprocess
import time
from pathlib import Path


PORT = int(os.environ.get("BROWSER_DEBUG_PORT", "9223"))
BROWSER_BIN = os.environ.get("BROWSER_BIN", "chromium")
RESTART_EXISTING = os.environ.get("BROWSER_RESTART_EXISTING", "1").lower() not in {"0", "false", "no"}


def port_open(port: int) -> bool:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.settimeout(0.5)
        return sock.connect_ex(("127.0.0.1", port)) == 0


def matching_browser_pids(profile_dir: Path) -> list[int]:
    try:
        output = subprocess.check_output(["ps", "-axo", "pid=,command="], text=True)
    except Exception:
        return []
    profile_arg = f"--user-data-dir={profile_dir}"
    matches: list[int] = []
    for line in output.splitlines():
        line = line.strip()
        if not line or profile_arg not in line:
            continue
        pid_text = line.split(None, 1)[0]
        try:
            pid = int(pid_text)
        except ValueError:
            continue
        if pid > 1 and pid != os.getpid():
            matches.append(pid)
    return matches


def wait_for_port_closed(port: int, timeout: float) -> bool:
    deadline = time.time() + timeout
    while time.time() < deadline:
        if not port_open(port):
            return True
        time.sleep(0.25)
    return not port_open(port)


def stop_owned_browser(profile_dir: Path) -> int:
    pids = matching_browser_pids(profile_dir)
    if not pids:
        return 0
    for pid in pids:
        try:
            os.kill(pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
    wait_for_port_closed(PORT, 10)
    if port_open(PORT):
        for pid in pids:
            try:
                os.kill(pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
        wait_for_port_closed(PORT, 5)
    return len(pids)


def start_browser(profile_dir: Path) -> str:
    subprocess.Popen(
        [
            BROWSER_BIN,
            f"--remote-debugging-port={PORT}",
            f"--user-data-dir={profile_dir}",
            "--no-first-run",
            "--no-default-browser-check",
        ],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
    )
    deadline = time.time() + 20
    while time.time() < deadline and not port_open(PORT):
        time.sleep(0.25)
    if not port_open(PORT):
        raise RuntimeError(f"Browser did not start on 127.0.0.1:{PORT}")
    return f"Browser started on 127.0.0.1:{PORT}"


def main() -> int:
    profile_dir = Path(os.environ["CRONPLUS_BROWSER_USER_DATA_DIR"])
    profile_dir.mkdir(parents=True, exist_ok=True)
    if port_open(PORT):
        if not RESTART_EXISTING:
            summary = f"Browser already listening on 127.0.0.1:{PORT}"
            stopped = 0
        else:
            pids = matching_browser_pids(profile_dir)
            if not pids:
                raise RuntimeError(
                    f"Port 127.0.0.1:{PORT} is open, but no browser process matched {profile_dir}; refusing to kill an unrelated process."
                )
            stopped = stop_owned_browser(profile_dir)
            if port_open(PORT):
                raise RuntimeError(f"Browser on 127.0.0.1:{PORT} did not stop cleanly")
            summary = start_browser(profile_dir)
    else:
        stopped = 0
        summary = start_browser(profile_dir)

    print(
        "CRONPLUS_RESULT="
        + json.dumps(
            {
                "status": "success",
                "summary": summary,
                "data": {"debug_port": PORT, "profile_dir": str(profile_dir), "stopped_processes": stopped},
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
