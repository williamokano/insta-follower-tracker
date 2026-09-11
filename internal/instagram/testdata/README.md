# Test fixtures

Every file here is **hand-written with invented accounts and dates**. They
reproduce the *structure* of Instagram's downloads — paths, file names, document
shapes, the wording of the date-range banner — and nothing else.

Real exports are read-only reference material. Never commit one, or any excerpt
of one, even temporarily: they list real people's handles, and a follower list
is their data rather than the account owner's.

The layouts reproduced here, all observed across genuine downloads:

| Fixture | Mirrors |
| --- | --- |
| `followers_array.json` | JSON export, bare top-level array |
| `followers_wrapped.json` | JSON export, `relationships_followers` wrapper |
| `followers_part2.json` | a second part of a split follower list |
| `followers_1.html` | HTML export |
| `start_here_windowed.html` | the summary page of a download restricted to a date range |
| `start_here_full.html` | the same page for a complete download |
| `not_an_export.json` | unrelated JSON, to check rejection |
