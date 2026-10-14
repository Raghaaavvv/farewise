#!/usr/bin/env python3
"""Generate a OneMap token locally; store only the token in ignored backend/.env."""
import argparse
from datetime import datetime, timezone
import getpass
import json
import os
from pathlib import Path
import re
import sys
import tempfile
import time
from urllib.error import HTTPError, URLError
from urllib.request import Request, HTTPRedirectHandler, build_opener

PROJECT_ROOT = Path(__file__).resolve().parent.parent
AUTH_URL = "https://www.onemap.gov.sg/api/auth/post/getToken"
TOKEN_PATTERN = re.compile(r"[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+")


class NoAuthRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        # Credentials are intended solely for the documented OneMap endpoint.
        return None


def parse_token(body, now=None):
    """Reject malformed tokens before writing anything to a shell-sourced file."""
    if not isinstance(body, dict):
        raise ValueError("OneMap returned an invalid token response.")
    token = body.get("access_token")
    if not isinstance(token, str) or not TOKEN_PATTERN.fullmatch(token):
        raise ValueError("OneMap returned an invalid access token.")
    expiry = body.get("expiry_timestamp")
    if isinstance(expiry, bool) or not isinstance(expiry, (str, int)):
        raise ValueError("OneMap returned an invalid token expiry.")
    try:
        expiry = int(expiry)
        valid_until = datetime.fromtimestamp(expiry, timezone.utc)
    except (ValueError, OverflowError, OSError):
        raise ValueError("OneMap returned an invalid token expiry.") from None
    if expiry <= (time.time() if now is None else now):
        raise ValueError("OneMap returned an expired token. Try generating another.")
    return token, valid_until


def save_token(path, token):
    if not TOKEN_PATTERN.fullmatch(token):
        raise ValueError("Refusing to save an invalid token.")
    previous = path.read_text() if path.exists() else "# Local backend configuration; ignored by Git.\n"
    settings = {"FAREWISE_ROUTING_PROVIDER": "onemap", "ONEMAP_TOKEN": token}
    lines = []
    for line in previous.splitlines():
        match = re.match(r"\s*(?:export\s+)?(FAREWISE_ROUTING_PROVIDER|ONEMAP_TOKEN)\s*=", line)
        if not match:
            lines.append(line)
    lines.extend(f"{key}={value}" for key, value in settings.items())
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", dir=path.parent, prefix=".env.", delete=False) as file:
            temporary = Path(file.name)
            os.chmod(temporary, 0o600)
            file.write("\n".join(lines) + "\n")
        os.replace(temporary, path)
    finally:
        if temporary and temporary.exists():
            temporary.unlink()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.parse_args()
    if not sys.stdin.isatty():
        raise ValueError("Run this setup command in an interactive terminal so the password stays hidden.")
    print("First register at https://www.onemap.gov.sg/apidocs/.")
    print("Credentials go to OneMap over HTTPS. Only the returned token is saved locally.")
    email = input("Registered OneMap email: ").strip()
    password = getpass.getpass("OneMap password (hidden): ")
    if not email or not password:
        raise ValueError("Email and password are required.")
    request = Request(AUTH_URL, data=json.dumps({"email": email, "password": password}).encode(),
                      headers={"Content-Type": "application/json", "Accept": "application/json"}, method="POST")
    try:
        with build_opener(NoAuthRedirect()).open(request, timeout=15) as response:
            raw = response.read(65537)
            if len(raw) > 65536:
                raise ValueError("OneMap returned an oversized authentication response.")
            body = json.loads(raw)
    except HTTPError as error:
        raise ValueError(f"OneMap authentication failed (HTTP {error.code}). Check your registered credentials.") from None
    except (URLError, TimeoutError, json.JSONDecodeError):
        raise ValueError("Could not obtain a valid OneMap response. Check your connection and try again.") from None
    token, expiry = parse_token(body)
    save_token(PROJECT_ROOT / "backend" / ".env", token)
    print(f"OneMap routing enabled in backend/.env. Token expires {expiry:%Y-%m-%d %H:%M UTC}.")
    print("Restart the running Farewise terminal: Ctrl+C, then bash scripts/dev.sh.")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError) as error:
        raise SystemExit(str(error)) from None
    except (KeyboardInterrupt, EOFError):
        raise SystemExit("Setup canceled; no new token saved.") from None
