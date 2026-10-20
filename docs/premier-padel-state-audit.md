# Premier Padel: event state and availability

## Behavior

- A live event takes priority over completed events.
- Discovery first reads the official Premier Padel partner page, with the general
  events page as a transport/structure fallback. Provider event titles may include
  sponsor/year suffixes and whitespace variations.
- Completed events are checked newest first until a playable replay is found.
  An individual event lookup failure does not prevent selecting a valid candidate;
  when no candidate can be verified, unresolved API failures remain errors.
- Channels expose `broadcast_state` (`live` or `replay`) independently of the
  source synchronization status. Existing non-Red Bull channels remain unchanged.
- A successful replay synchronization clears previous source errors. The dashboard
  therefore does not count a finished, playable event as a failed source.
- If a successful catalog lookup yields no candidate, previously saved videos are
  checked individually. Playable live/replay channels are retained. Upstream errors
  are not hidden by this fallback; failed synchronization preserves saved channels.
- Synchronization refreshes provider names, artwork and stream identifiers through
  the existing importer, respecting metadata locks and enabled settings for matching
  channels. No transcoding or AceStream playback behavior is changed.
- The overview and channel list distinguish live/replay; the session monitor uses
  the neutral label “En reproducción”.
- A successful lookup with no available content becomes `idle`, not an error.
  Previously stored but unavailable videos are marked `unavailable`, not deleted.
- Single-channel Premier Padel sources preserve the local channel ID and existing
  TVG ID when the upstream match changes. A metadata lock does not freeze the video
  endpoint. Multi-channel schedules are matched individually, not merged arbitrarily.
- The interface checks sync-job transitions every three seconds while visible,
  shows queued/running work, and reloads source/channel/overview data on completion.
  Background tabs and overlapping poll cycles are skipped.

## Verification

Regression coverage includes replay import, clearing historical errors, dashboard
failed-source counts, a video missing from the catalog, unavailable replay rejection,
real upstream failures preserving channels, live-event priority, latest replay
selection, new-event metadata refresh, stable exported IDs, metadata locks,
unavailable-newest fallback, unrelated event failures, idle versus broken discovery,
frontend state labels and automatic refresh on synchronization completion.

Run `go test -race ./...`, `go vet ./...` and
`node --test scripts/workspace.test.cjs`.

Optional public-catalog verification (isolated temporary database, no production
mutations): `ACEBRIDGE_TEST_REDBULL_REMOTE=1 go test ./internal/app -run
'^TestRedBullRemoteCatalog$' -v -count=1`.

Availability during synchronization follows Red Bull's `playable` metadata. It does
not guarantee uninterrupted playback or bypass geographic restrictions. A finished
event is not itself a playback error; transport/API errors remain visible.
