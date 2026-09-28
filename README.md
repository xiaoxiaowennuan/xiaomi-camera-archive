# Xiaomi Camera Archive

Xiaomi Camera Archive is a self-hosted web application for indexing, browsing, and playing historical Xiaomi/Mijia camera recordings. It keeps original media read-only and stores its SQLite index and browser-compatible transcode cache in a separate state directory.

This is an unofficial community project and is not affiliated with or endorsed by Xiaomi.

## Features

- Responsive desktop and mobile playback UI
- Day picker, 24-hour recording/event timeline, seek, playback speed, and continuous segment playback
- Motion and person event navigation with indexed JPEG thumbnails
- Source HEVC playback with automatic H.264/AAC compatibility fallback
- Multiple archive folders inside a deployment-controlled media library
- Built-in authentication, administrator-managed users, and server-side sessions
- Read-only media access through opaque database IDs; no path-based media API

## Architecture

The application is a single Go service backed by SQLite. It serves a React/TypeScript frontend and runs `ffprobe` while indexing and `ffmpeg` only when a browser needs a compatibility copy.

```text
read-only media library -> scanner -> SQLite index -> authenticated HTTP API -> responsive web UI
                                      state directory -> H.264/AAC compatibility cache
```

Passwords are hashed with Argon2id. HTTP sessions use the open-source [SCS](https://github.com/alexedwards/scs) session manager and are stored in SQLite. The bootstrap administrator password is read from a Docker secret file and is never stored in this repository.

## Requirements

- Docker Engine with Docker Compose v2, or Go 1.25+, Node.js 24+, pnpm, ffmpeg, and ffprobe for local development
- A dedicated directory containing one or more Mijia archive folders
- Each archive folder may use `MIJIA_RECORD_VIDEO`, direct hourly directories, or flat `NN_START_END.mp4` files; `MIJIA_RECORD_MOTION` is optional

Do not mount an entire disk, system directory, home directory, or Docker socket. Mount only a dedicated camera archive library.

## Recognized recording layout

The current indexer recognizes the directory layout produced by the tested Xiaomi/Mijia camera archive. Each configured archive folder is expected to look like this:

```text
camera-archive/
├── MIJIA_RECORD_VIDEO/
│   └── YYYYMMDDHH/
│       ├── mmMssS_UNIX.mp4
│       └── mmMssS_UNIX.jpeg
└── MIJIA_RECORD_MOTION/          # optional
    └── YYYYMMDDHH/
        └── motion_record_msg
```

For example, a segment beginning at 03:04:05 is represented as:

```text
MIJIA_RECORD_VIDEO/2024010203/04M05S_1704135845.mp4
MIJIA_RECORD_VIDEO/2024010203/04M05S_1704135845.jpeg
```

The final filename field is a 10-digit Unix timestamp and is treated as the segment start time. The `YYYYMMDDHH`, minute, and second fields must agree with that timestamp in the configured timezone. Files that do not match this rule are ignored rather than guessed.

The JPEG thumbnail is optional. Motion metadata is optional and, when present, must use the observed `IMIEVENT` binary layout: a 128-byte header followed by 32-byte records. The indexer ignores symlinks, hidden metadata, `.cached` files, unknown extensions, and unrelated log files. It currently does not connect directly to a camera, import cloud recordings, or support arbitrary NVR layouts.

## Docker setup

Create the private deployment configuration:

```sh
cp deploy/.env.example deploy/.env
mkdir -p deploy/secrets
chmod 700 deploy/secrets
printf '%s\n' 'replace-with-a-long-random-password' > deploy/secrets/bootstrap_admin_password
chmod 600 deploy/secrets/bootstrap_admin_password
```

Edit `deploy/.env`:

```dotenv
MEDIA_LIBRARY_ROOT=/path/to/read-only/media-library
INITIAL_FOLDER_NAME=Default archive
INITIAL_FOLDER_PATH=/path/to/read-only/media-library/first-archive
BOOTSTRAP_ADMIN_PASSWORD_FILE=./secrets/bootstrap_admin_password
APP_BIND_IP=127.0.0.1
APP_PORT=18080
SECURE_COOKIES=false
```

`MEDIA_LIBRARY_ROOT` is mounted once as `/media-library:ro`. Folder records may only reference this directory or its descendants, so adding a folder takes effect immediately and does not require a container restart. Changing the media library root itself requires recreating the container.

Build and start:

```sh
./scripts/start.sh --build
```

The initial account is `admin`; its password is the value in the bootstrap secret file. The secret is used only when the user table is empty and does not reset an existing administrator.

Operational commands:

```sh
./scripts/start.sh       # start an existing image
./scripts/restart.sh     # recreate the application container
./scripts/stop.sh        # stop without deleting state
./scripts/scan.sh        # rescan every configured archive folder
./scripts/set-password.sh admin  # apply the password currently stored in the secret file
```

Set `SECURE_COOKIES=true` when the application is served over HTTPS. Keep the default loopback bind unless a trusted reverse proxy or private network provides the access boundary.

To rotate a password, replace `deploy/secrets/bootstrap_admin_password` with the new value and run `./scripts/set-password.sh <username>`. The command hashes the password with Argon2id, updates the existing user, and invalidates active sessions. Passwords must contain 8–128 characters.

## Folder management

After login, the folder page is the default entry point. Administrators can add or edit archive folders using their server paths. The service converts each server path to a location under the read-only media-library mount and rejects paths outside the configured root, symlink escapes, missing directories, and folders without a recognized Xiaomi recording layout.

A valid saved folder starts indexing in the background immediately. Regular users can view folder names and playback status but cannot see server paths or use management APIs.

## Local development

Build the frontend and run the service with synthetic or non-sensitive media:

```sh
pnpm --dir web build

go run ./cmd/mijia-archive serve \
  --media-library-root /path/to/mounted/library \
  --media-library-host-root /path/to/mounted/library \
  --initial-folder-name "Test archive" \
  --initial-folder-path /path/to/mounted/library/test-archive \
  --state-dir /tmp/mijia-archive-state \
  --bootstrap-admin-password-file /path/to/private/password-file \
  --secure-cookies=false \
  --web-dir web/dist
```

Validation commands:

```sh
GOCACHE=/tmp/mijia-archive-go-cache go test ./...
GOCACHE=/tmp/mijia-archive-go-cache go vet ./...
pnpm --dir web test
pnpm --dir web build
pnpm --dir web test:e2e
git diff --check
```

Tests use generated temporary data and do not require or copy real recordings.

## Security notes

- Original recordings are always treated as immutable and must be mounted read-only.
- Database, sessions, transcodes, and temporary files live under the writable state directory.
- Every page, API, thumbnail, source video, and compatibility video requires an authenticated session; only the login page, static assets, and health check are public.
- State-changing requests require a same-origin browser request, and session cookies are HttpOnly and SameSite=Strict.
- Absolute server paths are visible only to administrators and are never accepted by media playback endpoints.
- Do not commit `.env`, password files, databases, sessions, caches, or real media.

See [the architecture](docs/architecture.md), [security model](docs/security.md), and [Mijia format notes](docs/mijia-format.md) for implementation details.

## License

This project is released under the [MIT License](LICENSE).
