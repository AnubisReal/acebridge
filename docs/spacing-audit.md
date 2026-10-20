# Interface spacing audit

Reviewed Overview, Channels, Sources, Monitor, Jellyfin Output and Settings in
desktop and mobile layouts. Scope is spacing/readability, not source credentials,
stream configuration, source synchronization or deletion.

- Channel headers/rows share grid tracks and explicit inter-column spacing. The
  hover playback word is replaced by a small decorative play icon; playback button
  names remain descriptive. Cell text can shrink without invading adjacent columns.
- Header styling is neutral rather than a heavy colored strip; output heading is
  aligned with output controls. Mobile keeps its stacked channel layout.
- Provider group/source values wrap within their cells. Replay labels also appear
  in the managed channel list, not only on Overview.
- Form headings, optional annotations, field help and checkbox descriptions use
  consistent separation and readable line heights.
- Source header/footer actions and monitor row text have explicit gaps and handling
  for longer values. Source counts use singular/plural labels.
- Red Bull source help and player heading no longer imply every video is live.
- Small mobile header controls preserve readable labels at 320px.

Manual responsive checks: 1440px desktop, 1024px intermediate, 390px and 320px mobile.
All five secondary pages fit their viewport horizontally. Desktop channel header and
row grid tracks match, with a 24px gap and no identity/group overlap. No configuration
forms were submitted and no channels/sources were removed during visual checks.

Regression commands: `node --test scripts/workspace.test.cjs`, `go test -race ./...`,
`go vet ./...`, `git diff --check`.
