"""Exercise a built Windows executable in an isolated, disposable user profile."""

import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request


def available_port():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def smoke(binary):
    with tempfile.TemporaryDirectory(prefix="cronplus-windows-smoke-") as temp:
        profile = Path(temp)
        env = os.environ.copy()
        env["USERPROFILE"] = str(profile)
        env.pop("CRONPLUS_PORT", None)
        port = available_port()
        base_url = f"http://127.0.0.1:{port}"
        config = profile / ".config" / "cronplus"
        # Bypass any machine-wide HTTP proxies when probing the local daemon.
        http = urllib.request.build_opener(urllib.request.ProxyHandler({}))

        for attempt in range(2):
            log_path = profile / f"daemon-{attempt}.log"
            with log_path.open("w+") as log:
                daemon = subprocess.Popen(
                    [str(binary), "--port", str(port)],
                    env=env,
                    stdout=log,
                    stderr=subprocess.STDOUT,
                    creationflags=subprocess.CREATE_NEW_PROCESS_GROUP,
                )
                try:
                    deadline = time.monotonic() + 30
                    while True:
                        if daemon.poll() is not None:
                            raise AssertionError("daemon exited during startup")
                        try:
                            with http.open(base_url + "/api/auth/check", timeout=1) as response:
                                token = json.load(response)["token"]
                            break
                        except (urllib.error.URLError, TimeoutError):
                            if time.monotonic() >= deadline:
                                raise AssertionError("daemon did not become ready")
                            time.sleep(0.1)

                    request = urllib.request.Request(
                        base_url + "/api/status",
                        headers={"Authorization": "Bearer " + token},
                    )
                    with http.open(request, timeout=5) as response:
                        status = json.load(response)
                    assert status["version"].startswith("windows-preview-"), status
                    assert status["tasks"]["total"] == 0, status
                    assert (config / "state.db").is_file(), "SQLite state is missing"
                    with http.open(base_url + "/", timeout=5) as response:
                        assert b"<html" in response.read().lower(), "embedded UI is missing"

                    cli = subprocess.run(
                        [str(binary), "status"], env=env,
                        capture_output=True, text=True, timeout=10,
                    )
                    assert cli.returncode == 0, cli.stderr
                    assert json.loads(cli.stdout)["version"] == status["version"]

                    duplicate = subprocess.run(
                        [str(binary), "--port", str(available_port())], env=env,
                        capture_output=True, text=True, timeout=10,
                    )
                    assert duplicate.returncode != 0, "second daemon acquired the same lock"
                    assert "another CronPlus daemon is already running" in duplicate.stderr, duplicate.stderr

                    daemon.send_signal(signal.CTRL_BREAK_EVENT)
                    assert daemon.wait(timeout=15) == 0, "graceful shutdown failed"
                    print(f"Windows daemon {'startup' if attempt == 0 else 'restart'}: API, UI, SQLite, CLI, locking and shutdown passed")
                finally:
                    if daemon.poll() is None:
                        daemon.kill()
                        daemon.wait(timeout=10)
                    log.seek(0)
                    print(log.read())


if __name__ == "__main__":
    if sys.platform != "win32":
        raise SystemExit("This smoke test requires Windows")
    smoke(Path(sys.argv[1]).resolve(strict=True))
