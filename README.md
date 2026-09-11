# Instagram Follower Tracker

Track how your Instagram follower list changes over time, using Instagram's own
**Download your information** export. Nothing is scraped and no credentials are
needed: you upload the file Instagram gives you, and the service keeps the
history.

<!-- markdownlint-disable-next-line MD033 -->
Self-hosted, single binary, SQLite, no authentication.

## How it works

1. Upload an export. The file is stored and queued; the response comes back
   immediately and processing happens in the background.
2. The first upload for an account is the **baseline**. There is nothing before
   it, so it records the follower list and no diff. This is a known limitation
   of starting from a single point in time.
3. Every upload after that is compared against the one before it, producing a
   list of who followed and who unfollowed.

So ten uploads give nine per-execution diffs, plus an overall comparison between
any two of them.

### Why there are four lists, not one

Adding up the unfollow events from each execution answers the wrong question.
Somebody can unfollow at execution 3 and follow again at execution 7: they show
up in a per-execution diff, but they are not actually gone.

The overall diff therefore compares two snapshots directly and splits the result:

| Bucket | Meaning |
| --- | --- |
| **Lost** | In the first snapshot, not in the last. Genuinely gone. |
| **Gained** | Not in the first snapshot, in the last. |
| **Returned** | In both, but unfollowed somewhere in between and came back. |
| **Transient** | Seen only in the middle: absent at both ends. |

**Lost** is the honest answer to "who is not following me anymore". The raw
unfollow log stays available at `/api/accounts/{handle}/unfollowers`, and it
deliberately over-reports; its response says so.

## Running it

### Docker Compose

```yaml
services:
  insta-follower-tracker:
    image: ghcr.io/williamokano/insta-follower-tracker:latest
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      PUID: 1000
      PGID: 1000
    volumes:
      - ./data:/data
```

Then open <http://localhost:8080>.

### Docker

```sh
docker run -d --name insta-follower-tracker \
  -p 8080:8080 \
  -e PUID="$(id -u)" -e PGID="$(id -g)" \
  -v "$PWD/data:/data" \
  ghcr.io/williamokano/insta-follower-tracker:latest
```

### From source

```sh
make build
IFT_DATA_DIR=./.localdata ./bin/ift
```

## User and group

The image runs as root only long enough to set up the identity, then drops
privileges. Two mechanisms are supported:

- **`PUID` / `PGID`** (default `1000:1000`). The data directory is chowned to
  match on startup, so bind-mounted files stay readable on the host.
- **`docker run --user 1001:1001`**. The container is already unprivileged, so
  the entrypoint leaves everything alone and starts the service directly. Make
  sure the mounted directory is writable by that user.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `IFT_DATA_DIR` | `/data` | SQLite database and uploaded exports. Use `/config` if you prefer. |
| `IFT_ADDR` | `:8080` | Listen address. |
| `IFT_MAX_UPLOAD_BYTES` | `104857600` | Maximum upload size (100 MiB). |
| `IFT_RETAIN_UPLOADS` | `true` | Keep raw exports after processing so they can be reprocessed. |
| `IFT_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `PUID` / `PGID` | `1000` | User and group to run as. Container only. |

Everything lives under `IFT_DATA_DIR`: the database at `tracker.db` and the raw
uploads under `uploads/`. Mounting that one directory is enough to persist the
whole installation.

## Getting the export from Instagram

1. Instagram → **Settings** → **Accounts Centre** → **Your information and
   permissions** → **Download your information**.
2. Request a download. Select **Followers and following** (you can request
   everything, the rest is ignored). **JSON** format is preferred; HTML exports
   are not supported.
3. When the archive arrives, upload the ZIP as-is. Alternatively, unzip it and
   upload `connections/followers_and_following/followers_1.json`.

Large accounts get the list split across `followers_1.json`, `followers_2.json`
and so on. Uploading the ZIP handles that automatically.

## Interface

- **`/`** — upload form, the account list, and the execution history with the
  follower count and the follow/unfollow totals for each. Expanding a row shows
  exactly who. The table refreshes itself while an upload is still processing.
- **`/accounts/{handle}/diff`** — the overall comparison, defaulting to the
  first and latest executions, with the four buckets described above.

## HTTP API

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/api/uploads` | Multipart upload: `account` and `file`. Answers `202` with the queued execution. |
| `GET` | `/api/uploads/{id}` | One execution, including its processing status. |
| `GET` | `/api/uploads/{id}/changes` | That execution's diff. Filter with `?type=followed` or `?type=unfollowed`. |
| `GET` | `/api/accounts` | Every tracked account with headline counts. |
| `GET` | `/api/accounts/{handle}/uploads` | The account's executions. |
| `GET` | `/api/accounts/{handle}/followers` | The current follower list. |
| `GET` | `/api/accounts/{handle}/diff` | Overall diff. `?from=` and `?to=` accept `first`, `last` or an execution id; defaults to `first` and `last`. |
| `GET` | `/api/accounts/{handle}/unfollowers` | Raw unfollow event log across all executions. Over-reports by design. |
| `GET` | `/healthz` | Health, version, and the number of uploads still queued. |

```sh
# Upload an export
curl -F account=your.handle -F file=@instagram-export.zip \
  http://localhost:8080/api/uploads

# Who is actually gone
curl -s 'http://localhost:8080/api/accounts/your.handle/diff' |
  jq '.lost[].username'
```

## Notes and limitations

- **The first upload has no diff.** It is the starting point.
- **Executions are ordered by when they were processed**, not by any date inside
  the export. Upload your exports oldest first.
- **Instagram handles are treated case-insensitively** and stored lowercased.
- **The export does not name its owner**, which is why the account handle is
  asked for at upload time. One instance can track several accounts.
- **HTML-format exports are not supported.** Request JSON.

## Development

```sh
make test        # go test ./...
make test-race   # with the race detector and coverage
make lint        # golangci-lint
make build       # static binary into bin/
make docker      # build the image locally
```

Commits follow [Conventional Commits](https://www.conventionalcommits.org/);
this is enforced on pull requests. Releases are cut by
[semantic-release](https://semantic-release.gitbook.io/) on merge to `main`,
which derives the version, writes the changelog, tags the release and publishes
the multi-arch image to GHCR as `X.Y.Z`, `X.Y`, `X` and `latest`.

> **One-time setup:** GHCR creates new packages as private. After the first
> successful release, open the package in GitHub and change its visibility to
> public. No workflow permission can do this for you.

## Licence

MIT. See [LICENSE](LICENSE).
