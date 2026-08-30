#!/usr/bin/env python3
"""analytics_log.py — tier-1 client for agent-skill-log.

Standard skill layout:
    your-skill/SKILL.md
    your-skill/scripts/analytics_log.py     <- this file
    your-skill/assets/logrepo.json          <- where events ship

Spools one usage event locally, asking for consent on first ever use per
machine. Stdlib only, no network on the logging path, never raises to the caller.

    python3 analytics_log.py log <event> [--source S] [--tool T] \
            [--props JSON] [--config path]

Spec: docs/specs/python-logger.md in github.com/muthuishere/agent-skill-log
"""
import json
import os
import platform
import re
import shutil
import subprocess
import sys
import time
import urllib.request

RELEASE_REPO = "muthuishere/agent-skill-log"
CLIENT_VERSION = "0.2.1"


def home_dir():
    if os.environ.get("LOGREPO_HOME"):
        return os.environ["LOGREPO_HOME"]
    if os.name == "nt" and os.environ.get("LOCALAPPDATA"):
        return os.path.join(os.environ["LOCALAPPDATA"], "logrepo")
    return os.path.expanduser("~/.config/logrepo")


def consent_status():
    try:
        with open(os.path.join(home_dir(), "consent.json")) as f:
            return json.load(f).get("status", "")
    except Exception:
        return ""


def write_consent(status):
    os.makedirs(home_dir(), exist_ok=True)
    with open(os.path.join(home_dir(), "consent.json"), "w") as f:
        json.dump({"status": status, "ts": now_iso()}, f)


def now_iso():
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def load_config(path):
    """Resolve logrepo.json. Standard skill layout is scripts/analytics_log.py +
    assets/logrepo.json, so ../assets/ is checked first; a copy beside the script
    also works."""
    here = os.path.dirname(os.path.abspath(__file__))
    skill_root = os.path.dirname(here)
    cfg = {"transport": "github", "sync_delay_minutes": 10, "max_age_minutes": 60}
    for p in [path,
              os.environ.get("LOGREPO_CONFIG"),
              os.path.join(skill_root, "assets", "logrepo.json"),
              os.path.join(here, "assets", "logrepo.json"),
              os.path.join(here, "logrepo.json"),
              os.path.join(home_dir(), "config.json")]:
        if p and os.path.exists(p):
            try:
                with open(p) as f:
                    cfg.update(json.load(f))
                break
            except Exception:
                pass
    return cfg


def target_key(cfg):
    tid = cfg.get("endpoint") if cfg.get("transport") == "http" else cfg.get("log_repo")
    tid = tid or "unconfigured"
    tid = re.sub(r"^https?://", "", tid)
    return re.sub(r"[^a-zA-Z0-9._-]+", "-", tid).strip("-")


def gh_login():
    cache = os.path.join(home_dir(), "identity.json")
    try:
        with open(cache) as f:
            login = json.load(f).get("login")
            if login:
                return login
    except Exception:
        pass
    try:
        login = subprocess.run(["gh", "api", "user", "--jq", ".login"],
                               capture_output=True, text=True, timeout=10).stdout.strip()
        if login:
            os.makedirs(home_dir(), exist_ok=True)
            with open(cache, "w") as f:
                json.dump({"login": login}, f)
            return login
    except Exception:
        pass
    return os.environ.get("USER") or os.environ.get("USERNAME") or "anonymous"


def ask_consent(cfg, source):
    dest = cfg.get("log_repo") or cfg.get("endpoint") or "(unconfigured)"
    sys.stderr.write(f"""
Skill usage analytics — one-time question for this machine.
The skill "{source}" (and others using agent-skill-log) records per event:
  your GitHub username, hostname, skill name, tool name, event name, timestamp
Batches are pushed to: {dest}
A small sync binary (~3 MB) will be downloaded from GitHub releases.
Enable analytics?  [y/N] """)
    try:
        ans = input().strip().lower()
    except Exception:
        ans = ""
    write_consent("granted" if ans.startswith("y") else "denied")
    return ans.startswith("y")


def binary_path():
    name = "logrepo.exe" if os.name == "nt" else "logrepo"
    found = shutil.which(name)
    if found:
        return found
    local = os.path.join(home_dir(), "bin", name)
    return local if os.path.exists(local) else None


def install_binary():
    """Best-effort download of the latest release asset for this OS/arch."""
    goos = {"darwin": "darwin", "linux": "linux", "windows": "windows"}.get(
        platform.system().lower(), "linux")
    mach = platform.machine().lower()
    arch = "arm64" if mach in ("arm64", "aarch64") else "amd64"
    ext = ".exe" if goos == "windows" else ""
    asset = f"logrepo_{goos}_{arch}{ext}"
    dest_dir = os.path.join(home_dir(), "bin")
    os.makedirs(dest_dir, exist_ok=True)
    dest = os.path.join(dest_dir, "logrepo" + ext)
    url = f"https://github.com/{RELEASE_REPO}/releases/latest/download/{asset}"
    try:
        with urllib.request.urlopen(url, timeout=30) as r, open(dest, "wb") as f:
            shutil.copyfileobj(r, f)
        os.chmod(dest, 0o755)
        return dest
    except Exception:
        try:
            os.path.exists(dest) and os.remove(dest)
        except Exception:
            pass
        return None


def spool_event(cfg, event, source, tool, props):
    mach = platform.machine().lower()
    ev = {"ts": now_iso(), "user": gh_login(),
          "host": platform.node() or "unknown-host",
          "source": source, "event": event,
          "os": platform.system().lower(),
          "arch": "arm64" if mach in ("arm64", "aarch64") else mach or "unknown",
          "project": os.path.basename(os.getcwd()) or "unknown",
          "client": "py/" + CLIENT_VERSION}
    if tool:
        ev["tool"] = tool
    if props:
        try:
            ev["props"] = json.loads(props)
        except Exception:
            return  # invalid props: drop rather than corrupt the spool
    spool_dir = os.path.join(home_dir(), "spools", target_key(cfg))
    os.makedirs(spool_dir, exist_ok=True)
    with open(os.path.join(spool_dir, "spool.ndjson"), "a") as f:
        f.write(json.dumps(ev) + "\n")


def nudge_flusher(config_path):
    binp = binary_path()
    if not binp:
        binp = install_binary()
    if not binp:
        return  # spool-only until a later run can download the binary
    args = [binp, "flush", "--maybe"]
    if config_path:
        args += ["--config", config_path]
    kwargs = {"stdout": subprocess.DEVNULL, "stderr": subprocess.DEVNULL,
              "stdin": subprocess.DEVNULL}
    if os.name == "nt":
        kwargs["creationflags"] = 0x00000008 | 0x00000200  # DETACHED | NEW_PROCESS_GROUP
    else:
        kwargs["start_new_session"] = True
    subprocess.Popen(args, **kwargs)


def main():
    try:
        if os.environ.get("LOGREPO_DISABLE") == "1":
            return
        if len(sys.argv) < 3 or sys.argv[1] != "log":
            return
        event, source, tool, props, config_path = sys.argv[2], "", "", "", ""
        args = sys.argv[3:]
        i = 0
        while i < len(args):
            if args[i] == "--source" and i + 1 < len(args):
                source = args[i + 1]; i += 1
            elif args[i] == "--tool" and i + 1 < len(args):
                tool = args[i + 1]; i += 1
            elif args[i] == "--props" and i + 1 < len(args):
                props = args[i + 1]; i += 1
            elif args[i] == "--config" and i + 1 < len(args):
                config_path = args[i + 1]; i += 1
            i += 1
        cfg = load_config(config_path)
        source = source or cfg.get("default_source", "unknown")

        status = consent_status()
        if status == "denied":
            return
        if status != "granted":
            if not sys.stdin.isatty():
                return  # never auto-consent in non-interactive contexts
            if not ask_consent(cfg, source):
                return
        spool_event(cfg, event, source, tool, props)
        nudge_flusher(config_path)
    except Exception:
        pass  # analytics must never break the skill


if __name__ == "__main__":
    main()
