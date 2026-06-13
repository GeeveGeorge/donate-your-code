# Canonicalization & `record_id` — the cross-language keystone

`record_id` is the SHA-256 of a canonical serialization of a **content-only
subset** of a record (the *preimage*). The Go client and the Python server **must
produce byte-identical canonical bytes** for the same logical preimage, or
content-addressing, dedup, and cross-contributor corroboration all break.

This file is the normative spec. It is CODEOWNERS-gated. Any change here is a
schema change and must bump `schema_version`.

## 1. Canonical JSON (RFC 8785 / JCS, restricted profile)

Serialize a value tree as JSON with:

- **UTF-8** output, no BOM.
- **Object keys sorted** ascending by Unicode code point. (All preimage keys are
  ASCII, where code-point order equals byte order. The only non-ASCII keys
  possible are inside a tool input, which is pre-serialized to an opaque string —
  see §3 — so the server never sorts non-ASCII keys.)
- **No insignificant whitespace** (no spaces after `:` or `,`).
- **Strings** escaped per RFC 8785 §3.2.2.2: escape `"`→`\"`, `\`→`\\`,
  U+0008→`\b`, U+0009→`\t`, U+000A→`\n`, U+000C→`\f`, U+000D→`\r`; other control
  chars U+0000–U+001F → `\u00xx` (lowercase hex); every other code point is
  emitted as literal UTF-8.
- **Numbers**: only integers and `json.Number`-style literals occur in the
  preimage. Integers are emitted in shortest decimal form. **Floats are
  forbidden** in the preimage value tree (reject, don't coerce). This avoids
  implementing the ECMAScript number algorithm in two languages.
- **Booleans/null**: `true` / `false` / `null`.

`record_id = lowercase_hex(sha256(canonical_bytes_of_preimage))`.

The server additionally asserts the file is named `<record_id>.json` and lives at
`staging/<record_id[0:2]>/<record_id[2:4]>/<record_id>.json`.

## 2. The preimage (content-only)

The preimage is built from the record as:

```
{
  "schema_version": <string>,
  "model":          <string>,            // "claude-fable-5"
  "messages": [ <message> ... ]
}
```

`<message>` has a FIXED key set (no omitempty — defaults are emitted):

```
{
  "role":        <string>,               // "user" | "assistant" | "tool"
  "model":       <string>,               // "" unless assistant
  "stop_reason": <string>,               // "" unless present
  "usage": {
    "cache_creation_input_tokens": <int>,   // 0 if absent
    "cache_read_input_tokens":     <int>,   // 0 if absent
    "service_tier":                <string> // "" if absent
  },
  "blocks": [ <block> ... ]
}
```

`<block>` shape depends on `type`:

- text / thinking / fallback: `{ "type": <t>, "text": <string> }`
- tool_use: `{ "type":"tool_use", "ref":<int>, "name":<string>, "input_json":<string> }`
- tool_result: `{ "type":"tool_result", "ref":<int>, "is_error":<bool>, "truncated":<bool>, "content":[ <block> ... ] }`
- image: `{ "type":"image", "image":<string> }`   // placeholder «IMAGE:sha256:bytes»

`ref` is `-1` when a tool_use id could not be linked.

### Fields EXCLUDED from the preimage (metadata / provenance, NOT identity)

`record_id`, `contributor`, `dco`, `license`, `provenance`, `client_version`,
`scrubber_version`, `claude_code_version`, `is_subagent`, `parent_session_hash`,
`models_present`, `redaction_summary`.

Excluding these is deliberate: the **same conversation donated by two people must
hash to the same `record_id`** (exact dedup + corroboration). `is_subagent` and
`parent_session_hash` are provenance/linkage, not content, so they are excluded
too.

## 3. `input_json` (tool_use inputs)

A tool_use `input` is arbitrary JSON. The client:

1. Parses it with number-preservation (`json.Number` / preserve literal).
2. Recursively scrubs every string leaf.
3. Canonicalizes the scrubbed tree to a JSON **string** using §1 (object keys
   sorted by byte order; `json.Number` literals preserved verbatim).

That string is stored as `input_json` and is treated as an **opaque string** by
both the preimage hashing and the server. The server does NOT re-canonicalize the
input's numbers — it hashes the stored string as-is. This removes any
cross-language number-formatting risk: the only numbers the server must format are
the integer token counts and `ref`s in the fixed preimage shape.

## 4. Conformance

`internal/record/canonical_test.go` holds golden vectors. The Python server ships
the same vectors and must produce identical bytes. CI fails on any divergence.
