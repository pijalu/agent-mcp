---
name: blender
description: Build, inspect, and render Blender scenes through the blender-mcp server using agent-mcp — headless CLI mode (bpy scripting on a .blend file) and live GUI add-on mode. Use for 3D scene creation, data-block inspection, and rendering stills to PNG.
---

# Blender via agent-mcp

Drive Blender through `blender-mcp` using the `agent-mcp` CLI. Never embed an
MCP client; shell out to `agent-mcp` instead.

## Prerequisites

1. Blender on `PATH` (`blender --version`). Override the executable with the
   `BLENDER_PATH` environment variable — the server reads it, not the shell.
2. `blender-mcp` Python package available to `uv` (a checkout works: `uv run
   --directory <checkout>/mcp blender-mcp`).
3. Server entry in `~/.agent-mcp/config.toml`:

   ```toml
   [mcpServers.blender]
     command = "uv"
     args = ["--directory", "/Users/muaddib/dev/blender_mcp/mcp", "run", "blender-mcp"]
     url = ""
     transport = "stdio"
   ```

   Register it without hand-editing (repeatable `--arg`; keep absolute paths —
   args are `exec`-ed, `$HOME` is **not** expanded):

   ```bash
   agent-mcp config add --name blender --command uv \
     --arg --directory --arg /Users/muaddib/dev/blender_mcp/mcp \
     --arg run --arg blender-mcp
   ```

4. GUI mode only: the Blender *MCP* add-on installed and enabled with its
   server started. The add-on listens on `localhost:9876`; override with
   `BLENDER_MCP_HOST` / `BLENDER_MCP_PORT`.

A config change needs a daemon restart to take effect:
`agent-mcp server shutdown` (idempotent), then re-issue the command.

## Two execution modes

| Tool suffix | Runs in | Needs add-on | Persists to disk |
|---|---|---|---|
| (none), e.g. `execute_blender_code` | the **running** Blender GUI via socket `:9876` | yes | only when the user saves / code saves |
| `_for_cli`, e.g. `execute_blender_code_for_cli` | `blender --background <blend_file> --python-expr` | no | only if code calls `bpy.ops.wm.save_as_mainfile` |

`_for_cli` tools are one-shot: every call re-opens the `.blend` from disk, so
**each call must redo its own setup and save its result**. Interactive tools
mutate the live session; use them when a human has Blender open.

## Tool inventory (26 tools)

```bash
agent-mcp list-tools --server blender --limit 100   # default page shows only 10
```

- Code: `execute_blender_code{code}`, `execute_blender_code_for_cli{blend_file,code}`
- Read-only summaries: `get_blendfile_summary_{datablocks,missing_files,of_linked_libraries,path_info,usage_guess}` plus `_for_cli` variants taking `{blend_file}`
- Scene queries (GUI): `get_objects_summary`, `get_object_detail_summary{name}`
- Docs: `get_python_api_docs{identifier}`, `search_api_docs{query}`, `search_manual_docs{query}`
- Screenshots (GUI): `get_screenshot_of_window_as_image`, `get_screenshot_of_area_as_image{area_ui_type}`, `get_screenshot_of_window_as_json`
- Navigation (GUI): `jump_to_tab_by_name{name}`, `jump_to_tab_by_space_type{space_type}`, `jump_to_view3d_object_by_name{name}`, `jump_to_view3d_object_data_by_name{name}`
- Render (GUI): `render_thumbnail_to_path{output_path}`, `render_viewport_to_path{output_path}`

Only 4 of these have a pure headless form: `execute_blender_code_for_cli` and
the `_for_cli` summaries. For headless rendering, render **inside**
`execute_blender_code_for_cli` via `bpy.ops.render.render(write_still=True)`.

## Command patterns

Long `code` payloads must be JSON-encoded; build the argument object with a
real JSON encoder rather than quoting by hand:

```bash
python3 -c "
import json
code = open('/tmp/scene.py').read()
print(json.dumps({'blend_file': '/abs/project/scene.blend', 'code': code}))
" > /tmp/args.json

agent-mcp call-tool --server blender --tool execute_blender_code_for_cli \
  --args "$(cat /tmp/args.json)" --call-timeout 180s

# Cheap read-only probes (no code payload)
agent-mcp call-tool --server blender --tool get_blendfile_summary_datablocks_for_cli \
  --args '{"blend_file":"/abs/project/scene.blend"}'
agent-mcp call-tool --server blender --tool get_blendfile_summary_path_info_for_cli \
  --args '{"blend_file":"/abs/project/scene.blend"}'
```

`--call-timeout` is the **client** timeout (default 30s). The server's internal
`blender --background` subprocess is capped at 120s and is not configurable, so
keep headless renders inside that budget. Raise `--call-timeout` beyond 120s to
get the server's real error instead of a client timeout.

## Reading results

- Tool returning a dict → `.data.structuredContent` (plus a JSON string in `.data.content[0].text`).
- Tool returning text → `.data.content[0].text`.
- Tool-level failure → `.data.isError == true` **with exit code 0**. Branch on
  `isError` (or `jq -e '.data.isError // false | not'`); a zero exit only means
  the CLI itself worked.

## Recipe: headless scene build + render (verified)

`execute_blender_code_for_cli` needs an existing `.blend` — Blender aborts
before Python runs when the path is missing. Seed an empty file once:

```bash
mkdir -p /abs/project
blender --background --python-expr "
import bpy
bpy.ops.wm.read_factory_settings(use_empty=True)
bpy.ops.wm.save_as_mainfile(filepath='/abs/project/scene.blend')
"
```

Then send one code block that builds the scene, saves, and renders:

```python
import bpy, math
from mathutils import Vector

OUT_PNG = "/abs/project/render.png"
OUT_BLEND = "/abs/project/scene.blend"

bpy.ops.wm.read_factory_settings(use_empty=True)
scene = bpy.context.scene

# Physically based sky. Blender 5.x sky types: MULTIPLE_SCATTERING (default),
# SINGLE_SCATTERING, PREETHAM, HOSEK_WILKIE — "NISHITA" no longer exists.
world = bpy.data.worlds.new("Sky")
scene.world = world
world.use_nodes = True
nt = world.node_tree
nt.nodes.clear()
sky = nt.nodes.new("ShaderNodeTexSky")
sky.sky_type = "MULTIPLE_SCATTERING"
sky.sun_elevation = math.radians(6.0)
sky.sun_rotation = math.radians(200.0)
sky.sun_intensity = 1.0
sky.altitude = 300.0
bg = nt.nodes.new("ShaderNodeBackground")
bg.inputs["Strength"].default_value = 1.0
wout = nt.nodes.new("ShaderNodeOutputWorld")
nt.links.new(sky.outputs["Color"], bg.inputs["Color"])
nt.links.new(bg.outputs["Background"], wout.inputs["Surface"])

bpy.ops.mesh.primitive_uv_sphere_add(radius=1.0, segments=96, ring_count=48,
                                     location=(0.0, 0.0, 1.0))
sphere = bpy.context.active_object
sphere.name = "MetalSphere"
bpy.ops.object.shade_smooth()

mat = bpy.data.materials.new("ChromedSteel")
mat.use_nodes = True
bsdf = mat.node_tree.nodes["Principled BSDF"]
bsdf.inputs["Base Color"].default_value = (0.92, 0.93, 0.96, 1.0)
bsdf.inputs["Metallic"].default_value = 1.0
bsdf.inputs["Roughness"].default_value = 0.08
sphere.data.materials.append(mat)

cam_data = bpy.data.cameras.new("Cam")
cam_data.lens = 60.0
cam = bpy.data.objects.new("Cam", cam_data)
scene.collection.objects.link(cam)
cam.location = Vector((3.2, -5.2, 2.3))
cam.rotation_euler = (Vector((0.0, 0.0, 1.0)) - cam.location).to_track_quat("-Z", "Y").to_euler()
scene.camera = cam

scene.render.engine = "CYCLES"      # EEVEE needs a GPU context; unreliable headless
scene.cycles.device = "CPU"
scene.cycles.samples = 64           # 64 + denoise ≈ 4s at 960x540 on Apple Silicon
scene.cycles.use_denoising = True
scene.render.resolution_x = 960
scene.render.resolution_y = 540
scene.render.image_settings.file_format = "PNG"
scene.render.filepath = OUT_PNG
scene.view_settings.view_transform = "AgX"

bpy.ops.wm.save_as_mainfile(filepath=OUT_BLEND)   # persist: nothing is saved for you
bpy.ops.render.render(write_still=True)

result = {"blend": OUT_BLEND, "png": OUT_PNG, "engine": scene.render.engine,
          "objects": [o.name for o in scene.objects]}
```

Verify with MCP (not by trusting the render call), then inspect the file:

```bash
agent-mcp call-tool --server blender --tool get_blendfile_summary_path_info_for_cli \
  --args '{"blend_file":"/abs/project/scene.blend"}'      # is_saved, size, backups
sips -g pixelWidth -g pixelHeight /abs/project/render.png   # 960 / 540
```

Sanity-check pixels instead of eyeballing (sky ↔ metal reflection split):

```bash
blender --background --python-expr "
import bpy
img = bpy.data.images.load('/abs/project/render.png')
w, h = img.size
px = list(img.pixels)
def mean(x0, x1, y0, y1):
    acc = [0.0, 0.0, 0.0]; n = 0
    for y in range(y0, y1, 4):
        for x in range(x0, x1, 4):
            i = (y * w + x) * 4
            acc = [acc[0] + px[i], acc[1] + px[i+1], acc[2] + px[i+2]]; n += 1
    return [round(c / n, 3) for c in acc]
print('sky', mean(0, 200, h - 120, h), 'sphere', mean(w//2 - 60, w//2 + 60, h//2 - 60, h//2 + 60))
"
```

Reference output from the verified run of this exact recipe (960x540, Cycles
CPU, 64 spp): `sky [0.563, 0.614, 0.690]` (blue-biased gradient) and
`sphere [0.801, 0.823, 0.848]` (bright neutral = mirror of the sky). Treat these
as shape, not values — adding a noise/bump normal to the metal and setting
`scene.view_settings.look = "AgX - Punchy"` darkens both to about
`sky [0.444, 0.504, 0.592]`, `sphere [0.734, 0.761, 0.791]`.

## Interactive mode specifics (verified)

The add-on runs tool code in a *restricted* context, so code written for
headless mode can fail here:

- `bpy.ops.wm.read_factory_settings(...)` is **blocked** by the add-on sandbox —
  use `bpy.ops.wm.read_homefile(use_empty=True, use_factory_startup=True)`.
- `bpy.context.active_object` may be absent and operator context is thin. Build
  objects with the data API instead of operators:

  ```python
  import bmesh, bpy
  mesh = bpy.data.meshes.new("SphereMesh")
  bm = bmesh.new()
  bmesh.ops.create_uvsphere(bm, u_segments=96, v_segments=48, radius=1.0)
  bm.to_mesh(mesh); bm.free()
  mesh.shade_smooth()                       # not bpy.ops.object.shade_smooth()
  obj = bpy.data.objects.new("MetalSphere", mesh)
  bpy.context.scene.collection.objects.link(obj)
  ```

- To bring the bridge up in a GUI Blender (no manual clicks), enable the add-on
  `bl_ext.lab_blender_org.mcp`, then call
  `mcp_to_blender_server.start(prefs.host, prefs.port)` and register
  `execute_interactive.run` with `bpy.app.timers.register`.
- The add-on exposes no MCP resources or prompts (`list-resources` and
  `list-prompts` are empty); API/manual documentation is served by the
  `get_python_api_docs` / `search_api_docs` / `search_manual_docs` tools.
- `render_thumbnail_to_path` / `render_viewport_to_path` write into Blender's
  scratch dir, not the directory you pass: the output path becomes
  `<bpy.app.tempdir>/blender_mcp/<basename>` and the returned `filepath` is
  authoritative. Thumbnails clamp the longest side to 320 px (e.g. 320x180 for a
  960x540 scene).
- Image content survives the hop intact; with `--format text` it renders as
  `[image: image/png]` instead of spilling base64.

## Pitfalls

- **Missing `blend_file`**: `blender --background <path>` aborts before Python,
  surfacing as `No result marker in Blender output ... Cannot read file`. Create
  the file first; never pass a path you have not `stat`-ed.
- **`result` must be a dict**: assign `result = {"key": value}`. Scalars, lists,
  and Blender objects fail with a `TypeError` from the wrapper.
- **No implicit save**: `_for_cli` runs discard scene state when Blender exits.
  Multiple calls do not compose — either save and re-open, or do everything in
  one call.
- **Dirty-file snapshots**: when the GUI has the same file open with unsaved
  changes, the server saves a temporary `scene_mcp_0001.blend` copy and deletes
  it afterwards. Write output paths explicitly; do not rely on `bpy.data.filepath`.
- **Interactive tools without the add-on** return
  `Cannot connect to Blender at localhost:9876. Ensure Blender is running with
  the MCP addon enabled...`. Fall back to the `_for_cli` variant.
- **`list-tools` paginates at 10**: pass `--limit 100`, and treat an empty
  `nextCursor` as end-of-list.
- **Prefer absolute paths** for `blend_file`, `OUT_PNG`, `render_thumbnail_to_path`.
- **Blender 5.x API drift**: optional `use_nodes` assignments emit
  `DeprecationWarning` (removed in 6.0) but still work; `ShaderNodeTexSky` types
  differ from 4.x (see recipe comment).
- **Exit codes do not report tool errors**: check `.data.isError`.
- **Large payloads need a current `agent-mcp`**: results above gRPC's 4 MiB
  default used to fail with
  `daemon call: rpc error: code = ResourceExhausted desc = grpc: received message
  larger than max`. The daemon hop is now bounded at 64 MiB in each direction, so
  a current binary delivers big screenshots, mesh dumps, and doc reads; seeing
  `ResourceExhausted` means the installed `agent-mcp` predates that fix
  (`go install ./cmd/agent-mcp`).

## Local end-to-end test

```bash
agent-mcp list-servers | jq '.data.servers[] | select(.name=="blender")'
agent-mcp list-tools --server blender --limit 100 | jq '.data.tools | length'   # 26
agent-mcp call-tool --server blender --tool get_blendfile_summary_datablocks_for_cli \
  --args '{"blend_file":"/abs/project/scene.blend"}' | jq '.data.structuredContent.datablock_counts'
```

## Cleanup

```bash
agent-mcp server shutdown    # idempotent; ends Blender/mcp child processes
```

For throwaway calls use `--no-server` so the MCP process is torn down with that
invocation.
