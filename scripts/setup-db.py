#!/usr/bin/env python3
"""Create private local PostgreSQL settings without printing the password."""
from pathlib import Path
import argparse
import os
import secrets

ROOT = Path(__file__).resolve().parent.parent

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--port', type=int, default=55432)
    args = parser.parse_args()
    if not 1024 <= args.port <= 65535:
        raise SystemExit('Choose a database port between 1024 and 65535.')
    path = ROOT / 'backend' / 'db.env'
    try:
        descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError:
        print('Existing backend/db.env preserved; its password was not changed.')
        return
    password = secrets.token_hex(24)
    with os.fdopen(descriptor, 'w') as file:
        file.write('# Private local database settings; ignored by Git.\n')
        file.write('POSTGRES_USER=farewise\nPOSTGRES_DB=farewise\n')
        file.write(f'POSTGRES_PASSWORD={password}\nFAREWISE_DB_PORT={args.port}\n')
        file.write(f'DATABASE_URL=postgres://farewise:{password}@127.0.0.1:{args.port}/farewise?sslmode=disable\n')
    print('Created owner-only backend/db.env. Start PostgreSQL with bash scripts/db.sh up.')

if __name__ == '__main__':
    main()
