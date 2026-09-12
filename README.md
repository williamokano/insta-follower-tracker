# Instagram Follower Tracker

Track how your Instagram follower list changes over time, using Instagram's own
**Download your information** export. Nothing is scraped and no credentials are
needed: you upload the file Instagram gives you, and the service keeps the
history.

<!-- markdownlint-disable-next-line MD033 -->
Self-hosted, single binary, SQLite, no authentication.

## What it tracks

An export carries several relationship lists, and all of them are recorded and
compared over time in the same way:

| List | |
| --- | --- |
| **Followers** | accounts that follow you — required; an export without it is rejected |
| **Following** | accounts you follow |
| **Close friends** | your close friends list |
| **Pending requests** | follow requests you sent that are still unanswered |
| **Blocked**, **Restricted**, **Favourited** | as named |

Having both followers and following in the same export also answers two
questions that need no history at all, because they compare two lists at the
same moment: who you follow that **does not follow you back**, and who follows
you that you do not follow back.

`following_hashtags` is deliberately ignored: its entries are topics rather than
accounts.

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
   everything, the rest is ignored), and **set the date range to "All time"**.
   Either **JSON** or **HTML** format works. JSON is slightly better: it records
   the date each person followed you, which the HTML format renders as localised
   prose and so is not read.

   > **The date range is the one setting that matters.** A download limited to a
   > date range contains only the people who *started following inside that
   > window* — for a year-long window on an established account, that can be a
   > few dozen names instead of hundreds. Compared against a full snapshot it
   > reports everybody else as having unfollowed. Such uploads are refused, but
   > it is much easier to request the download correctly than to notice later.
3. When the archive arrives, upload the ZIP as-is — the account it belongs to is
   read out of it, so there is usually nothing to type. Alternatively, unzip it
   and upload the `followers_1` file from the `followers_and_following` folder
   (`.json` or `.html`, depending on the format you chose); a bare file like that
   names no account, so supply the handle yourself.

Large accounts get the list split across `followers_1.json`, `followers_2.json`
and so on. Uploading the ZIP handles that automatically.

## Interface

- **`/`** — upload form, the account list, and the execution history with the
  follower count and the follow/unfollow totals for each. Expanding a row shows
  exactly who followed and unfollowed, plus the complete list as it stood at that
  point, with a filter. The first upload has no diff but still shows its list.
  The table refreshes itself while an upload is still processing.
- **`/accounts/{handle}/diff`** — the overall comparison, defaulting to the
  first and latest executions, with the four buckets described above.

## HTTP API

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/api/uploads` | Multipart upload: `file`, plus optional `account` (read from the export when omitted), `allow_partial=1` to accept a date-limited export, and `snapshot_date=YYYY-MM-DD` to override the export date. Answers `202` with the queued execution. |
| `GET` | `/api/uploads/{id}` | One execution, including its processing status. |
| `GET` | `/api/uploads/{id}/changes` | That execution's diff. Filter with `?type=followed` or `?type=unfollowed`. |
| `GET` | `/api/uploads/{id}/followers` | The complete list that execution recorded, available even for the first one. |
| `GET` | `/api/uploads/{id}/relationships` | Who is not following back, and who you do not follow back, within one execution. |
| `GET` | `/api/lists` | The relationship lists this service understands. |
| `GET` | `/api/accounts` | Every tracked account with headline counts. |
| `GET` | `/api/accounts/{handle}/uploads` | The account's executions. |
| `GET` | `/api/accounts/{handle}/followers` | The current follower list. |
| `GET` | `/api/accounts/{handle}/diff` | Overall diff. `?from=` and `?to=` accept `first`, `last` or an execution id; defaults to `first` and `last`. |
| `GET` | `/api/accounts/{handle}/unfollowers` | Raw departure log across all executions. Over-reports by design. |

Every endpoint that reads a list takes `?list=` to choose which one, defaulting
to `followers`.
| `GET` | `/healthz` | Health, version, and the number of uploads still queued. |

```sh
# Upload an export. The account is read from the archive.
curl -F file=@instagram-you-2026-09-11-abc123.zip \
  http://localhost:8080/api/uploads

# Who is actually gone
curl -s 'http://localhost:8080/api/accounts/your.handle/diff' |
  jq '.lost[].username'
```

## Backfilling old exports

Uploads do not have to arrive in order. Each execution is placed by **when its
export was generated**, so an archive from two years ago can be added today and
will sort into its rightful place in the history.

Inserting an execution changes what the ones around it should be compared
against, so the whole account's history is recomputed after every upload. That
is exact rather than approximate, because each execution stores its complete
follower list rather than only its deltas — the same property that makes the
overall diff honest about people who left and came back.

Upgrading from a version before export dates were tracked needs nothing: on
first start the service re-reads the dates from the uploads it kept, so an
existing history sorts correctly and accepts backfills straight away. Exports
whose files were not retained keep the order they already had.

```sh
# Order of upload is irrelevant; order of export date is what counts.
curl -F account=your.handle -F file=@instagram-you-2024-07-12-abc.zip \
  http://localhost:8080/api/uploads
curl -F account=your.handle -F file=@instagram-you-2026-09-11-xyz.zip \
  http://localhost:8080/api/uploads
```

## Notes and limitations

- **The first upload has no diff.** It is the starting point, though its
  follower list is shown like any other execution's.
- **Executions are ordered by when the export was generated**, not by when it
  was uploaded, so exports can be added in any order. The date is read from the
  archive: from the generation time newer downloads state about themselves, then
  the archive's own timestamps, then the date in the file name. Set *Export date*
  on the upload form to override a wrong guess.
- **Instagram handles are treated case-insensitively** and stored lowercased.
- **The account is read from the export** where it says so: from the archive's
  file name, or from the summary page inside it if the file was renamed. Enter a
  handle only when neither does, or to override it — useful if you have renamed
  the account since the export was taken. One instance can track several
  accounts.
- **Date-limited exports are refused.** Two checks: the date range newer
  downloads declare in `start_here.html`, and a fall in follower count too steep
  to be real (more than half, on accounts above 25 followers). Tick *Accept a
  partial export*, or send `allow_partial=1`, to record one anyway.
- **One gap is known and not guessable.** A *first* upload, of a date-limited
  download, in a format old enough to declare no range, cannot be detected from
  the file alone — there is no baseline to compare it against and nothing in it
  says what it covers. Follow dates look like they would help and do not: a young
  or fast-growing account has the same shape as a filtered export of an old one.
- **A list that is absent is not a list that is empty.** An export that did not
  carry, say, the following list says nothing about it, and is skipped rather
  than recorded as everybody having left. An export that carries it *empty* does
  mean there is nobody in it, and is recorded as such.
- **Follow dates are only read from JSON exports.** The HTML format writes them
  in the account's own language, so they are left empty rather than guessed at.
  They are display metadata and never affect a diff.

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

The published package inherits this repository's visibility, because the image
carries an `org.opencontainers.image.source` label that links it to the repo, so
`v1.0.0` was pullable anonymously as soon as it was pushed. If you ever need to
change that, package visibility is set on the package's own settings page — it is
not something a workflow permission controls.

## Licence

MIT. See [LICENSE](LICENSE).
