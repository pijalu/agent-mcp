# agent-mcp bugs

## 1. Blender CLI tools discard the Python traceback, so any error inside user code is unreproducible from the MCP response

**Severity:** low (DX) — correctness of the tool is fine, debuggability is not.

**Where:** `blender_mcp/mcp/blmcp/tools_helpers/blender_cli.py`, `run_blender_cli()`, the
`except Exception as ex:` branch of the generated wrapper:

```python
except Exception as ex:
    print("__BLMCP_ERROR__" + json.dumps(str(ex)))
```

**Symptom:** only `str(ex)` reaches the client. The frames, line numbers and the
offending expression are lost.

Observed 2026-10-30 with `execute_blender_code_for_cli`:

```
{"error":{"code":"mcp","message":"mcp error: Error executing tool
 execute_blender_code_for_cli: Blender error: string indices must be integers, not 'str'"}}
```

The real cause was a single bad line in a ~400-line payload
(`r["tris"] for r in report` where `report` is a dict, so `r` is a `str`).
Because the message carries no file/line, the failing construct had to be found
by bisecting the payload (exec-file variant → works, 5-space re-indent → works,
original → fails), which cost several extra round-trips. Each probe also re-runs
Blender, so this multiplies wall-clock cost on heavy scenes.

**Expected:** include the formatted traceback, e.g.

```python
except Exception as ex:
    import traceback
    print("__BLMCP_ERROR__" + json.dumps(traceback.format_exc()))
```

The client already prints this message verbatim, so no protocol change is needed.
If a one-line message is desired for terminal output, send the traceback and let
the CLI render the last line as the summary.

**Notes / non-bugs confirmed while investigating:** payloads up to 20 KB execute
correctly through the daemon, and `execute_blender_code_for_cli` behaves
identically to calling `run_blender_cli()` directly, so no size limit or
transport-escaping bug was involved.

## 2. Screenshot results are unusable: the CLI drops the pixels, the library ships raw base64, no consumer can "see" the image

**Status:** fixed for items 1, 2, 4, 5 (see *Fix* at the bottom); item 3 is
now open entry 3.

**Severity:** high (functionality) — `take_screenshot`-style tools return
`image` content that no consumer can use, and text-based consumers pay a large
token/context cost for data they discard.

**Scope:** every binary MIME type, not only images. Any non-text content part
(`image/*`, `audio/*`, `video/*`, `application/pdf`, `application/octet-stream`,
embedded resource blobs) hits the same wall: the CLI masks it, the library
passes raw base64, nothing can access the bytes. Screenshot is the motivating
example; the mechanism below must be MIME-agnostic.

**Where:**

- `internal/cli/text.go`, `contentLine()`, the `case "image":` branch
  (line ~281): renders `[image: image/png]` and drops the data.
- `client/payload.go`, `Shape()` / `DropMirroredContent()`: image content
  passes through untouched; `raw` and default payloads both carry the full
  base64 string.
- `call-tool` command surface: no `--save-images DIR` (or equivalent) flag, so
  there is no way to persist the pixels to an artifact.

**Symptom (chrome-devtools-mcp `take_screenshot`, fullPage):**

```
agent-mcp call-tool --server chrome --tool take_screenshot \
  --args '{"pageId":1,"fullPage":true}'
→ [image: image/png]
```

The bytes are decoded on the wire, then thrown away. Nothing is saved, nothing
is rendered, no vision channel exists.

For library consumers the mirror problem: Goa's `mcp` tool marshals the whole
shaped result to JSON and returns it as the tool-result text
(`goa/tools/mcp.go`, `run()`), so the same screenshot arrives as a ~0.5–2 MB
base64 string — hundreds of thousands of tokens — only to be head/tail
truncated by the output budget. The model pays for data it never sees. This is
worse than the CLI case: it is a context bomb on every screenshot call.

**Expected:**

1. CLI: `call-tool --save-binaries DIR` (or an `--binaries-policy` flag)
   writes each binary content part to a file and prints the path plus
   mime/size, e.g.
   `[binary: image/png → /tmp/amcp/ab12.png (1440x900, 212 KiB)]` — the
   marker form is identical for any MIME type (extension derived from the
   MIME type, `application/octet-stream` falls back to `.bin`).
2. Vision/media support in the CLI: the CLI is itself driven by models, so it
   must let a vision- (or audio-) capable model access the content **without
   any consumer-side base64 decode step**. Default mode: write each binary
   part to a temp file and emit a pointer marker — `[binary: image/png →
   /tmp/amcp/ab12.png (1440x900, 212 KiB)]` — so stdout stays tiny. The
   calling harness recognizes the path and attaches the file natively
   (Goa's `EncodeImage` reads image paths directly; `read_media` covers
   image/video); the provider encodes it for the model. No base64 ever
   crosses the CLI boundary. Optional `--binaries-policy dataurl` emits
   `data:<mime>;base64,<payload>` inline for consumers that cannot share a
   filesystem with the CLI (remote harness): one form or the other, never
   both — `dataurl` → provider, temp file → artifact + pointer, `strip` →
   marker only.
3. ~~Secured HTTP artifact serving~~ — **moved to entry 3** (open; optional
   transport alongside the local file pointers).
4. Client library: injectable binary-content policy in `Shape()` —
   `keep` (default, back-compat) | `dataurl` (model-native form) |
   `file` (decode → temp file → replace part data with the path) | `strip`.
   Applies to every non-text part carrying inline base64 — `image`, `audio`,
   and `resource` contents with binary blobs — not just `type:"image"`.
   Policy is chosen by the embedding caller (Goa passes `dataurl` for vision
   models, `file` otherwise); the library stays generic, no chrome-specific
   code.
5. Goa side (tracked there, noted here for the contract): recognize
   `data:<mime>;...` URLs and binary content parts in MCP results, and route
   them by MIME type — images into `ToolResult.Images` (Goa's provider layer
   already accepts paths/data: URLs via `EncodeImage`, downscales via
   `FitImage`, and downgrades for text-only models); non-image binaries as
   artifact paths in the result text for the model to pass to `read_media`
   or shell tools. Replace the inline payload with the marker from (1).
   Vision models then see the screenshot automatically at ~1–2k token cost;
   the JSON payload stays small.

**Notes / non-goals:** server-side auto-captioning via a vision sub-call is
deliberately rejected — lossy, extra latency/cost, and strictly worse than a
native image block. For page *content* understanding the right tool remains
`take_snapshot` (a11y tree, plain text); screenshots are for visual/layout
verification. Full-page captures amplify the problem (PNG scales with page
height); element- or viewport-scoped captures keep artifacts small.

**Fix (shipped, library-first per direction):**

- Library (`client/binaries.go`): `BinaryPolicy` (`keep|dataurl|file|strip`),
  `ParseBinaryPolicy`, `ApplyBinariesPolicy` — a generic walk over any
  `data`/`blob` + `mimeType` pair (content parts, embedded resource parts,
  resource contents; `application/octet-stream` → `.bin`). Carriers:
  `Request{Binaries, BinaryStore}`; `BinaryStore` is a one-method interface
  defined at the point of use, so the library stays filesystem-free and the
  CLI is the only FS writer. Undecodable/unstoreable payloads degrade to
  `[binary: <mime> undecodable: …]` / `[binary: <mime> not saved: …]` without
  failing the call. Library default `keep` — byte-for-byte back-compat.
- CLI (`internal/cli`): `--binaries-policy` (default `file`) and
  `--save-binaries` on `call-tool` and `read-resource`. Artifacts go to the
  given dir or a fresh `agent-mcp-binaries-*` temp dir per invocation, written
  0600 as `part-NN.<ext>`; `agent-mcp-binaries-*` dirs older than 24h are
  swept on the next run (the short-lived CLI cannot clean its own artifacts,
  and harnesses read them after exit). Pointer marker:
  `[binary: image/png → /tmp/…/part-01.png (212 KiB)]` — MIME-derived
  extension plus size; the pixel dimensions from the spec example are
  intentionally omitted (decoding image headers is a rendering concern).
- Renderer safety: text output never prints `data:` URLs — the dataurl policy
  renders `[binary: <mime> dataurl (<size>)]` for every carrier (content
  parts and resource contents), so the context bomb cannot reach a terminal.
- The policy applies to `--raw` too: it is a caller-directed content
  transform, not payload shaping (pinned by test).
- Goa side (item 5) landed in the goa repo: MCP results with `data:` URLs and
  binary content parts route by MIME type into `ToolResult.Images` (vision
  models) or artifact paths (`read_media`); goa uses the library default
  (`keep`), so no interop break.
- Gates: client coverage 86.5%, cli 89.3% (≥80% required); `go test -race`,
  staticcheck, gocognit/gocyclo thresholds, gofmt — all clean. Docs: CLI help
  ("Binary content" section), spec §Binary Content Policy, CHANGELOG.

## 3. HTTP artifact serving for binary pointer markers (open, deferred)

**Severity:** medium (enhancement) — the pointer markers from entry 2 require
a shared filesystem between the CLI and the consumer. Remote harnesses
(containers, remote agents) and UIs (`<img src>` in the Goa WebUI, `<audio
src>`, download links) cannot read `/tmp/agent-mcp-binaries-*/part-01.png`.

**Idea (from entry 2 item 3):** the daemon already exposes protobuf on a
configurable host:port with optional API-key auth (`--api-key`), so a read-only
HTTP endpoint on the same daemon is a natural extension: `GET
http://127.0.0.1:PORT/artifacts/<id>.png` returns the stored bytes with the
stored `Content-Type` — any MIME type works identically (image, audio, video,
PDF, octet-stream); the ID's file extension derives from the MIME type at
store time. The CLI pointer marker then gains a URL form:
`[binary: image/png → /tmp/amcp/ab12.png | http://127.0.0.1:8765/artifacts/ab12.png?tok=…]`.

**Constraints:** loopback bind by default; opt-in flag (`--artifact-http`);
per-artifact unguessable capability token in the URL (short TTL, single
resource, no listing endpoint — a leaked URL expires with the daemon); lookup
by random ID only (no path construction, no traversal); MIME type served from
stored metadata, `Cache-Control: no-store`; artifacts die with the daemon
lifetime, mirroring the retained Evidence precedent; plus a per-artifact TTL
sweeper and daemon-shutdown cleanup — never silent unbounded growth.
