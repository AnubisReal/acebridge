# Overview layout verification

- Desktop (>1000px): viewport-bound overview with independently scrollable catalog
  and inspector. The actual header offset is measured after navigation, resize,
  header reflow and font loading.
- Five compact channel columns on large desktops, three on intermediate desktop,
  two on mobile. Short desktop windows scroll the complete catalog pane so its
  toolbar remains reachable. Mobile retains normal document scrolling.
- Inspector uses the same surface, borders and rounded components as channel cards.
- Catalog region is keyboard-focusable; existing playback is unchanged.

The catalog now starts with 60 channels and appends offset-based pages of 60 near
the scroll boundary. There is no “Show more” button or “View all” footer. Scrollbars
are visually hidden without disabling mouse, touch or keyboard scrolling. The pane
reaches the frame's inner bottom border; no footer covers the last rows. API failures
preserve loaded channels; overlapping requests, stale responses and duplicate IDs
are guarded. Query/mode changes reset pagination and scroll position.

Verified with the isolated `node scripts/overview-fixture.cjs` catalog (96 synthetic
channels, no production database). At 1440×900, loading 24 cards kept the document at
900px and the frame bottom at approximately 875px; content scrolled inside a 362px
catalog viewport. At 1100×768 the document remained 768px high. At 1280×600 the
document remained 600px high and catalog controls were reachable by internal scroll.
At 390×844 the mobile two-column catalog had no horizontal overflow.

Latest real-service check at 1440×900: 60 initial cards grew to 120 on keyboard
scroll, while document height stayed 900px. Catalog bottom was 874px and outer frame
bottom 875px. The old system footer is now a compact header disclosure.

Validation: `go test -race ./...`, `go vet ./...`, 22 Node regression tests and
`git diff --check` passed. Real local service uses ignored persistent `data/`;
existing channels were preserved. Publication uses the repository's container workflow;
updating a server stack is a separate operation.
