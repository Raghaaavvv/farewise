#!/usr/bin/env python3
"""Create private local PostgreSQL settings without printing the password."""

import argparse
import os
from pathlib import Path
import secrets

ROOT = Path(__file__).resolve().parent.parent


def main():
    """Validate the local port and create the settings file only when absent."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, default=55432)
    arguments = parser.parse_args()

    if not 1024 <= arguments.port <= 65535:
        raise SystemExit("Choose a database port between 1024 and 65535.")

    settings_path = ROOT / "backend" / "db.env"

    # Exclusive creation preserves an existing database password on later runs.
    # Mode 0600 keeps a newly created file readable and writable only by its owner.
    try:
        descriptor = os.open(
            settings_path,
            os.O_WRONLY | os.O_CREAT | os.O_EXCL,
            0o600,
        )
    except FileExistsError:
        print("Existing backend/db.env preserved; its password was not changed.")
        return

    password = secrets.token_hex(24)

    # Preserve the exact settings format consumed by Docker Compose and dev.sh.
    with os.fdopen(descriptor, "w") as settings_file:
        settings_file.write("# Private local database settings; ignored by Git.\n")
        settings_file.write("POSTGRES_USER=farewise\nPOSTGRES_DB=farewise\n")
        settings_file.write(
            f"POSTGRES_PASSWORD={password}\nFAREWISE_DB_PORT={arguments.port}\n"
        )
        settings_file.write(
            f"DATABASE_URL=postgres://farewise:{password}@127.0.0.1:"
            f"{arguments.port}/farewise?sslmode=disable\n"
        )

    print(
        "Created owner-only backend/db.env. "
        "Start PostgreSQL with bash scripts/db.sh up."
    )


if __name__ == "__main__":
    main()
