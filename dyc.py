#!/usr/bin/env python3
"""dyc — Donate Your Code (single-file, zero-dependency client).

A transparent Python port of the dyc client so coding agents can run it directly
(`python3 dyc.py scan`) without executing an opaque downloaded binary. Standard
library only. Same record format + RFC 8785 content-addressing as the Go client
and the server gate.

Commands:
  python3 dyc.py scan [--all] [--json]
  python3 dyc.py preview <selector> [more...] [--full] [--json]
  python3 dyc.py auth login [--token-stdin] | status | logout
  python3 dyc.py donate <selector> [more...] [--dry-run] [--yes]
  python3 dyc.py status

Security: scan/preview/donate --dry-run make NO network calls. donate's only
network destination is the GitHub API. Transcripts are read only behind a hard
allowlist (projects/**/*.jsonl + subagents + referenced tool-results).
"""
import hashlib
import json
import math
import os
import re
import subprocess
import sys
import time
import urllib.error
import urllib.request

VERSION = "0.2.0-py"
FABLE_MODEL = "claude-fable-5"
SCHEMA_VERSION = "dyc.record.v1"
STAGING_OWNER = os.environ.get("DYC_STAGING_OWNER", "GeeveGeorge")
STAGING_REPO = os.environ.get("DYC_STAGING_REPO", "donate-your-code-staging")

# ---------------------------------------------------------------------------
# Canonicalization (RFC 8785) + content-addressed record_id. MUST match the Go
# client (internal/record) and the server (scripts/canonical.py) byte-for-byte.
# ---------------------------------------------------------------------------

def _enc_string(s):
    out = ['"']
    for ch in s:
        o = ord(ch)
        if ch == '"':
            out.append('\\"')
        elif ch == '\\':
            out.append('\\\\')
        elif ch == '\b':
            out.append('\\b')
        elif ch == '\t':
            out.append('\\t')
        elif ch == '\n':
            out.append('\\n')
        elif ch == '\f':
            out.append('\\f')
        elif ch == '\r':
            out.append('\\r')
        elif o < 0x20:
            out.append('\\u%04x' % o)
        else:
            out.append(ch)
    out.append('"')
    return ''.join(out)


def _canon(v):
    if v is None:
        return "null"
    if v is True:
        return "true"
    if v is False:
        return "false"
    if isinstance(v, str):
        return _enc_string(v)
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, int):
        return str(v)
    if isinstance(v, float):
        raise ValueError("float not allowed in canonical preimage")
    if isinstance(v, list):
        return "[" + ",".join(_canon(e) for e in v) + "]"
    if isinstance(v, dict):
        keys = sorted(v.keys())
        return "{" + ",".join(_enc_string(k) + ":" + _canon(v[k]) for k in keys) + "}"
    raise TypeError("canonicalize: unsupported type %s" % type(v).__name__)


def canonicalize(v):
    return _canon(v).encode("utf-8")


def _ref_val(r):
    return -1 if r is None else int(r)


def _block_preimage(b):
    t = b.get("type")
    if t == "tool_use":
        return {"type": "tool_use", "ref": _ref_val(b.get("ref")),
                "name": b.get("name", ""), "input_json": b.get("input_json", "")}
    if t == "tool_result":
        return {"type": "tool_result", "ref": _ref_val(b.get("ref")),
                "is_error": bool(b.get("is_error", False)),
                "truncated": bool(b.get("truncated", False)),
                "content": [_block_preimage(x) for x in b.get("content", [])]}
    if t == "image":
        return {"type": "image", "image": b.get("image", "")}
    return {"type": t, "text": b.get("text", "")}


def build_preimage(rec):
    msgs = []
    for m in rec.get("messages", []):
        u = m.get("usage") or {}
        msgs.append({
            "role": m.get("role", ""),
            "model": m.get("model", ""),
            "stop_reason": m.get("stop_reason", ""),
            "usage": {
                "cache_creation_input_tokens": int(u.get("cache_creation_input_tokens", 0)),
                "cache_read_input_tokens": int(u.get("cache_read_input_tokens", 0)),
                "service_tier": u.get("service_tier", ""),
            },
            "blocks": [_block_preimage(b) for b in m.get("blocks", [])],
        })
    return {"schema_version": rec["schema_version"], "model": rec["model"], "messages": msgs}


def record_id(rec):
    return hashlib.sha256(canonicalize(build_preimage(rec))).hexdigest()


# ---------------------------------------------------------------------------
# Scrubbing — deterministic, fail-closed. Ported from the Go scrubber.
# ---------------------------------------------------------------------------

PRIVATE_KEY_RE = re.compile(r"-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----", re.S)
PGP_RE = re.compile(r"-----BEGIN PGP [A-Z ]*-----.*?-----END PGP [A-Z ]*-----", re.S)
SECRET_RULES = [
    (re.compile(r"AKIA[0-9A-Z]{16}"), "«SECRET:aws»"),
    (re.compile(r"gh[pousr]_[A-Za-z0-9]{36,}"), "«SECRET:github»"),
    (re.compile(r"github_pat_[A-Za-z0-9_]{22,}"), "«SECRET:github»"),
    (re.compile(r"xox[baprs]-[A-Za-z0-9-]{10,}"), "«SECRET:slack»"),
    (re.compile(r"https://hooks\.slack\.com/services/[A-Za-z0-9/]+"), "«SECRET:slack»"),
    (re.compile(r"(?:sk|rk)_live_[A-Za-z0-9]{16,}"), "«SECRET:stripe»"),
    (re.compile(r"AIza[0-9A-Za-z\-_]{35}"), "«SECRET:google»"),
    (re.compile(r"SG\.[A-Za-z0-9_\-]{22}\.[A-Za-z0-9_\-]{43}"), "«SECRET:sendgrid»"),
    (re.compile(r"sk-ant-[A-Za-z0-9_\-]{20,}"), "«SECRET:anthropic»"),
    (re.compile(r"sk-[A-Za-z0-9]{20,}"), "«SECRET:openai»"),
    (re.compile(r"eyJ[A-Za-z0-9_\-]+\.eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+"), "«SECRET:jwt»"),
    (re.compile(r"npm_[A-Za-z0-9]{36}"), "«SECRET:npm»"),
]
EMAIL_RE = re.compile(r"[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}")
MAC_RE = re.compile(r"\b(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}\b")
IPV4_RE = re.compile(r"\b(?:\d{1,3}\.){3}\d{1,3}\b")
IPV6_RE = re.compile(r"\b(?:[0-9A-Fa-f]{0,4}:){2,7}[0-9A-Fa-f]{0,4}\b")
PHONE_INTL_RE = re.compile(r"\+\d[\d \-().]{6,}\d")
PHONE_US_RE = re.compile(r"\b\d{3}[-.]\d{3}[-.]\d{4}\b")
CARD_RE = re.compile(r"\b(?:\d[ -]?){13,19}\b")
HOME_UNIX_RE = re.compile(r"(/Users/|/home/)([^/\s:\"']+)")
HOME_WIN_RE = re.compile(r"([A-Za-z]:\\Users\\)([^\\\s:\"']+)")
OTHER_PATH_RE = re.compile(r"/(?:usr|etc|var|opt|tmp|private|bin|sbin|Applications|Library|System|Volumes|mnt|srv|root)(?:/[\w.\-+@]+)+")
TOKEN_RE = re.compile(r"[A-Za-z0-9+/=_\-]{20,}")


def _entropy(s):
    if not s:
        return 0.0
    from collections import Counter
    n = len(s)
    return -sum((c / n) * math.log2(c / n) for c in Counter(s).values())


def _luhn(d):
    total, alt = 0, False
    for ch in reversed(d):
        x = ord(ch) - 48
        if alt:
            x *= 2
            if x > 9:
                x -= 9
        total += x
        alt = not alt
    return total % 10 == 0


class Scrubber:
    def __init__(self, usernames):
        seen, us = set(), []
        for u in usernames or []:
            if u and len(u) >= 3 and u not in seen:
                seen.add(u)
                us.append(u)
        self.usernames = sorted(us)

    def scrub(self, text, high_risk=False):
        c = {}
        def bump(k, n=1): c[k] = c.get(k, 0) + n
        discovered = set()

        def sub_count(rx, repl, key):
            nonlocal text
            text2, n = rx.subn(lambda m: repl, text)
            if n:
                bump(key, n)
            text = text2

        sub_count(PRIVATE_KEY_RE, "«PRIVATE_KEY»", "keys")
        sub_count(PGP_RE, "«PRIVATE_KEY»", "keys")
        for rx, repl in SECRET_RULES:
            sub_count(rx, repl, "secrets")

        def home_unix(m):
            if len(m.group(2)) >= 3:
                discovered.add(m.group(2))
            bump("paths")
            return "«HOME»/"
        text = HOME_UNIX_RE.sub(home_unix, text)

        def home_win(m):
            if len(m.group(2)) >= 3:
                discovered.add(m.group(2))
            bump("paths")
            return "«HOME»\\"
        text = HOME_WIN_RE.sub(home_win, text)
        sub_count(OTHER_PATH_RE, "«PATH»", "paths")

        thr = 3.2 if high_risk else 3.6
        def hi(m):
            tok = m.group(0)
            has_digit = any(ch.isdigit() for ch in tok)
            has_sym = any(ch in "+/=_-" for ch in tok)
            if (has_digit or has_sym) and _entropy(tok) >= thr:
                bump("high_entropy")
                return "«HIGH_ENTROPY»"
            return tok
        text = TOKEN_RE.sub(hi, text)

        sub_count(EMAIL_RE, "«EMAIL»", "emails")

        def card(m):
            digits = re.sub(r"\D", "", m.group(0))
            if 13 <= len(digits) <= 19 and _luhn(digits):
                bump("cards")
                return "«CARD»"
            return m.group(0)
        text = CARD_RE.sub(card, text)

        text = MAC_RE.sub(lambda m: (bump("macs"), "«MAC»")[1], text)

        def ipv4(m):
            parts = m.group(0).split(".")
            if all(p.isdigit() and 0 <= int(p) <= 255 for p in parts):
                bump("ips")
                return "«IP»"
            return m.group(0)
        text = IPV4_RE.sub(ipv4, text)

        sub_count(PHONE_INTL_RE, "«PHONE»", "phones")
        sub_count(PHONE_US_RE, "«PHONE»", "phones")

        names = sorted(set(self.usernames) | discovered)
        for n in names:
            rx = re.compile(r"\b" + re.escape(n) + r"\b")
            text, k = rx.subn("«USER»", text)
            if k:
                bump("usernames", k)
        return text, c

    def scrub_tree(self, v, high_risk=False):
        counts = {}
        def merge(d):
            for k, val in d.items():
                counts[k] = counts.get(k, 0) + val
        if isinstance(v, str):
            out, c = self.scrub(v, high_risk)
            merge(c)
            return out, counts
        if isinstance(v, list):
            for i in range(len(v)):
                v[i], c = self.scrub_tree(v[i], high_risk)
                merge(c)
            return v, counts
        if isinstance(v, dict):
            for k in list(v.keys()):
                v[k], c = self.scrub_tree(v[k], high_risk)
                merge(c)
            return v, counts
        return v, counts

    def tripwire(self, serialized):
        hits = []
        for m in ["-----BEGIN", "PRIVATE KEY", "AKIA", "ghp_", "gho_", "ghs_",
                  "ghu_", "github_pat_", "xox-", "sk-ant-"]:
            if m in serialized:
                hits.append(m)
        if HOME_UNIX_RE.search(serialized) or HOME_WIN_RE.search(serialized):
            hits.append("home-path")
        if EMAIL_RE.search(serialized):
            hits.append("email")
        for n in self.usernames:
            if re.search(r"\b" + re.escape(n) + r"\b", serialized):
                hits.append("username")
                break
        return hits


def scrubber_version():
    h = hashlib.sha256()
    h.update(b"dyc-scrub-py-v0")
    for rx, _ in SECRET_RULES:
        h.update(rx.pattern.encode())
    return "scrub-" + h.hexdigest()[:16]


def hash_image(raw):
    return "«IMAGE:%s:%d»" % (hashlib.sha256(raw).hexdigest(), len(raw))


# ---------------------------------------------------------------------------
# Discovery — config roots + hard allowlist + path safety.
# ---------------------------------------------------------------------------

def config_roots():
    cands = []
    if os.environ.get("CLAUDE_CONFIG_DIR"):
        for part in os.environ["CLAUDE_CONFIG_DIR"].split(os.pathsep):
            cands.append(os.path.join(part, "projects"))
    if os.environ.get("XDG_CONFIG_HOME"):
        cands.append(os.path.join(os.environ["XDG_CONFIG_HOME"], "claude", "projects"))
    else:
        cands.append(os.path.join(os.path.expanduser("~"), ".config", "claude", "projects"))
    cands.append(os.path.join(os.path.expanduser("~"), ".claude", "projects"))
    seen, roots = set(), []
    for c in cands:
        if os.path.isdir(c):
            real = os.path.realpath(c)
            if real not in seen:
                seen.add(real)
                roots.append(real)
    return roots


def usernames(encoded_dirs):
    out, seen = [], set()
    def add(n):
        if n and len(n) >= 3 and n not in seen:
            seen.add(n)
            out.append(n)
    try:
        import getpass
        add(getpass.getuser())
    except Exception:
        pass
    for enc in encoded_dirs:
        parts = enc.lstrip("-").split("-")
        for i in range(len(parts) - 1):
            if parts[i] in ("Users", "home") and parts[i + 1]:
                add(parts[i + 1])
    return out


def safe_open(path, allowed_root):
    """Open a file only if its real path is contained in allowed_root and it's a
    regular, non-symlink file."""
    clean = os.path.abspath(path)
    root_real = os.path.realpath(allowed_root)
    if os.path.islink(clean):
        raise IOError("symlink refused")
    real = os.path.realpath(clean)
    if real != root_real and not real.startswith(root_real + os.sep):
        raise IOError("path escapes allowed root")
    if not os.path.isfile(real) or os.path.islink(real):
        raise IOError("not a regular file")
    return open(real, "r", errors="replace")


def project_cwd(path, home):
    counts = {}
    try:
        f = safe_open(path, os.path.dirname(os.path.dirname(path)))
    except IOError:
        return ""
    with f:
        for i, line in enumerate(f):
            if i >= 8000:
                break
            line = line.strip()
            if not line:
                continue
            try:
                o = json.loads(line)
            except Exception:
                continue
            c = o.get("cwd")
            if c:
                counts[c] = counts.get(c, 0) + 1
    if not counts:
        return ""
    best = max(counts, key=lambda k: (counts[k], k != home))
    non_home = {k: v for k, v in counts.items() if k != home}
    if non_home:
        bnh = max(non_home, key=lambda k: counts[k])
        if best != home:
            return best
        if counts[bnh] * 100 >= counts[best] * 10:
            return bnh
    return best


def discover_sessions():
    home = os.path.expanduser("~")
    sessions = []
    for root in config_roots():
        try:
            proj_dirs = sorted(os.listdir(root))
        except OSError:
            continue
        for pd in proj_dirs:
            proj_dir = os.path.join(root, pd)
            if not os.path.isdir(proj_dir):
                continue
            try:
                entries = sorted(os.listdir(proj_dir))
            except OSError:
                continue
            for e in entries:
                if not e.endswith(".jsonl"):
                    continue
                sid = e[:-len(".jsonl")]
                main = os.path.join(proj_dir, e)
                if not os.path.isfile(main):
                    continue
                s = {
                    "root": root, "encoded": pd, "session": sid,
                    "main": main,
                    "tool_results": os.path.join(proj_dir, sid, "tool-results"),
                    "subagents": [],
                    "cwd": "", "name": _basename_from_encoded(pd),
                }
                sub_dir = os.path.join(proj_dir, sid, "subagents")
                if os.path.isdir(sub_dir):
                    for sf in sorted(os.listdir(sub_dir)):
                        if sf.endswith(".jsonl"):
                            s["subagents"].append(os.path.join(sub_dir, sf))
                cwd = project_cwd(main, home)
                if cwd:
                    s["cwd"] = cwd
                    s["name"] = os.path.basename(cwd.rstrip("/")) or s["name"]
                sessions.append(s)
    return sessions


def _basename_from_encoded(enc):
    enc = enc.lstrip("-")
    if "-" in enc:
        return enc.rsplit("-", 1)[1]
    return enc or "(root)"


def home_abbrev(p):
    home = os.path.expanduser("~")
    if p and p.startswith(home):
        return "~" + p[len(home):]
    return p


# ---------------------------------------------------------------------------
# Transcript + thread reconstruction → records.
# ---------------------------------------------------------------------------

MSG_ID_RE = re.compile(r"^msg_[A-Za-z0-9]+$")
PERSISTED_RE = re.compile(r"Full output saved to:\s*(\S+\.txt)")
MAX_EXTERNAL = 8 << 20


def is_genuine_fable(line):
    if line.get("type") != "assistant":
        return False
    m = line.get("message") or {}
    return m.get("model") == FABLE_MODEL and bool(MSG_ID_RE.match(m.get("id", "")))


def is_synthetic(line):
    if line.get("type") != "assistant":
        return False
    m = line.get("message") or {}
    return m.get("model") == "<synthetic>" or not MSG_ID_RE.match(m.get("id", ""))


def parse_lines(f):
    out = []
    for line in f:
        line = line.strip()
        if not line:
            continue
        try:
            out.append(json.loads(line))
        except Exception:
            continue
    return out


def count_fable(f):
    n = 0
    for line in f:
        if FABLE_MODEL not in line:
            continue
        try:
            o = json.loads(line.strip())
        except Exception:
            continue
        if is_genuine_fable(o):
            n += 1
    return n


def _content_blocks(content):
    """Return (blocks, plain, is_string)."""
    if content is None:
        return [], "", False
    if isinstance(content, str):
        return [], content, True
    if isinstance(content, list):
        return content, "", False
    return [], "", False


class _Converter:
    def __init__(self, scrub, tool_results_dir):
        self.s = scrub
        self.dir = tool_results_dir
        self.ref_by_tool = {}
        self.next_ref = 0
        self.counts = {}
        self.drop_reason = ""

    def _bump(self, c):
        for k, v in c.items():
            self.counts[k] = self.counts.get(k, 0) + v

    def _text_block(self, typ, text, high):
        out, c = self.s.scrub(text or "", high)
        self._bump(c)
        return {"type": typ, "text": out}

    def _assign_ref(self, tool_id):
        if tool_id in self.ref_by_tool:
            return self.ref_by_tool[tool_id]
        r = self.next_ref
        self.next_ref += 1
        if tool_id:
            self.ref_by_tool[tool_id] = r
        return r

    def _scrub_input(self, inp):
        if inp is None:
            return ""
        try:
            tree, c = self.s.scrub_tree(inp, True)
            self._bump(c)
            return _canon(tree)
        except Exception:
            out, c = self.s.scrub(json.dumps(inp), True)
            self._bump(c)
            return _canon(out)

    def convert(self, line):
        m = line.get("message")
        if not m:
            return None
        t = line.get("type")
        if t == "assistant":
            return self._assistant(m)
        if t == "user":
            return self._user(m)
        return None

    def _assistant(self, m):
        blocks, plain, is_str = _content_blocks(m.get("content"))
        rb = []
        if is_str:
            if plain:
                rb.append(self._text_block("text", plain, False))
        else:
            for b in blocks:
                bt = b.get("type")
                if bt == "text":
                    rb.append(self._text_block("text", b.get("text", ""), False))
                elif bt == "thinking":
                    rb.append(self._text_block("thinking", b.get("thinking") or b.get("text", ""), True))
                elif bt == "tool_use":
                    ref = self._assign_ref(b.get("id", ""))
                    rb.append({"type": "tool_use", "ref": ref, "name": b.get("name", ""),
                               "input_json": self._scrub_input(b.get("input"))})
                elif b.get("text"):
                    rb.append(self._text_block("fallback", b.get("text"), True))
        if not rb:
            return None
        msg = {"role": "assistant", "model": m.get("model", ""), "blocks": rb}
        if m.get("stop_reason"):
            msg["stop_reason"] = m["stop_reason"]
        u = m.get("usage")
        if u:
            msg["usage"] = {
                "cache_creation_input_tokens": int(u.get("cache_creation_input_tokens", 0)),
                "cache_read_input_tokens": int(u.get("cache_read_input_tokens", 0)),
                "service_tier": u.get("service_tier", ""),
            }
        return msg

    def _user(self, m):
        blocks, plain, is_str = _content_blocks(m.get("content"))
        if is_str:
            if not plain:
                return None
            return {"role": "user", "blocks": [self._text_block("text", plain, False)]}
        if any(b.get("type") == "tool_result" for b in blocks):
            return self._tool(blocks)
        rb = [self._text_block("text", b.get("text", ""), False)
              for b in blocks if b.get("type") == "text" and b.get("text")]
        if not rb:
            return None
        return {"role": "user", "blocks": rb}

    def _tool(self, blocks):
        rb = []
        for b in blocks:
            if b.get("type") != "tool_result":
                continue
            tid = b.get("tool_use_id", "")
            if tid not in self.ref_by_tool:
                continue
            sub = self._tool_content(b.get("content"))
            if sub is None:
                return None  # drop_reason set
            rb.append({"type": "tool_result", "ref": self.ref_by_tool[tid],
                       "is_error": bool(b.get("is_error", False)), "content": sub})
        if not rb:
            return None
        return {"role": "tool", "blocks": rb}

    def _tool_content(self, content):
        blocks, plain, is_str = _content_blocks(content)
        if is_str:
            mm = PERSISTED_RE.search(plain) if "<persisted-output>" in plain else None
            if mm:
                data = self._read_external(mm.group(1))
                if data is None:
                    self.drop_reason = "external tool-result unresolved"
                    return None
                return [self._text_block("text", data, True)]
            return [self._text_block("text", plain, True)]
        out = []
        for sb in blocks:
            if sb.get("type") == "text":
                out.append(self._text_block("text", sb.get("text", ""), True))
            elif sb.get("type") == "image":
                self._bump({"images": 1})
                out.append({"type": "image", "image": hash_image(json.dumps(sb.get("source", "")).encode())})
            elif sb.get("text"):
                out.append(self._text_block("fallback", sb.get("text"), True))
        return out

    def _read_external(self, path):
        try:
            f = safe_open(path, self.dir)
        except IOError:
            return None
        with f:
            data = f.read(MAX_EXTERNAL + 1)
        if len(data) > MAX_EXTERNAL:
            return None
        return data


def build_records(session, scrub, client_version):
    """Return list of (record, status, reason). Reads main + subagent files."""
    results = []
    for path, is_sub in [(session["main"], False)] + [(p, True) for p in session["subagents"]]:
        try:
            f = safe_open(path, session["root"])
        except IOError:
            continue
        with f:
            lines = parse_lines(f)
        rec, status, reason = _build_one(lines, is_sub, session, scrub, client_version)
        results.append((rec, status, reason))
    return results


def _main_thread(lines):
    by_uuid = {l.get("uuid"): l for l in lines if l.get("uuid")}
    anchor = None
    for l in lines:
        if is_genuine_fable(l):
            if anchor is None or l.get("timestamp", "") > anchor.get("timestamp", ""):
                anchor = l
    if anchor is None:
        return []
    chain, seen, cur = [], set(), anchor
    while cur is not None:
        u = cur.get("uuid")
        if u in seen:
            break
        seen.add(u)
        chain.append(cur)
        pu = cur.get("parentUuid")
        if not pu or pu not in by_uuid:
            break
        cur = by_uuid[pu]
    chain.reverse()
    return chain


def _build_one(lines, is_sub, session, scrub, client_version):
    chain = _main_thread(lines)
    if not chain:
        return None, "no-fable", ""
    conv = _Converter(scrub, session["tool_results"])
    msgs, models, ccv = [], set(), ""
    for l in chain:
        if is_synthetic(l):
            continue
        m = conv.convert(l)
        if m is None:
            if conv.drop_reason:
                return None, "dropped", conv.drop_reason
            continue
        if m["role"] == "assistant" and m.get("model"):
            models.add(m["model"])
        if l.get("version"):
            ccv = l["version"]
        msgs.append(m)
    if not msgs:
        return None, "no-fable", ""
    rec = {
        "schema_version": SCHEMA_VERSION, "record_id": "", "model": FABLE_MODEL,
        "provenance": "self-attested", "dco": False, "license": "CC0-1.0",
        "client_version": "dyc/" + client_version, "scrubber_version": scrubber_version(),
        "claude_code_version": ccv, "is_subagent": is_sub,
        "models_present": sorted(models), "messages": msgs,
        "redaction_summary": {k: conv.counts.get(k, 0) for k in
                              ["keys", "secrets", "high_entropy", "emails", "phones",
                               "cards", "ips", "macs", "paths", "usernames", "images"]},
    }
    rec["record_id"] = record_id(rec)
    hits = scrub.tripwire(json.dumps(rec, ensure_ascii=False))
    if hits:
        return None, "dropped", "tripwire: " + ",".join(hits)
    return rec, "ok", ""


def shard_path(rid):
    return "staging/%s/%s/%s.json" % (rid[:2], rid[2:4], rid)


# ---------------------------------------------------------------------------
# State (token + dedup).
# ---------------------------------------------------------------------------

def state_dir():
    base = os.environ.get("XDG_CONFIG_HOME") or os.path.join(os.path.expanduser("~"), ".config")
    d = os.path.join(base, "dyc")
    os.makedirs(d, exist_ok=True)
    return d


def save_token(tok):
    p = os.path.join(state_dir(), "token")
    with open(p, "w") as f:
        f.write(tok.strip())
    os.chmod(p, 0o600)


def resolve_token():
    p = os.path.join(state_dir(), "token")
    if os.path.exists(p):
        t = open(p).read().strip()
        if t:
            return t, "stored"
    for env in ["DYC_GITHUB_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"]:
        if os.environ.get(env, "").strip():
            return os.environ[env].strip(), "env:" + env
    return "", ""


def gh_cli_token():
    try:
        return subprocess.check_output(["gh", "auth", "token"], stderr=subprocess.DEVNULL).decode().strip()
    except Exception:
        return ""


def load_donated():
    p = os.path.join(state_dir(), "donated.jsonl")
    out = {}
    if os.path.exists(p):
        for line in open(p):
            line = line.strip()
            if line:
                try:
                    e = json.loads(line)
                    out[e["record_id"]] = e
                except Exception:
                    pass
    return out


def append_donated(entry):
    with open(os.path.join(state_dir(), "donated.jsonl"), "a") as f:
        f.write(json.dumps(entry) + "\n")


# ---------------------------------------------------------------------------
# GitHub REST client (urllib).
# ---------------------------------------------------------------------------

class GitHub:
    API = "https://api.github.com"

    def __init__(self, token):
        self.token = token

    def _req(self, method, path, body=None):
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(self.API + path, data=data, method=method)
        req.add_header("Authorization", "Bearer " + self.token)
        req.add_header("Accept", "application/vnd.github+json")
        req.add_header("X-GitHub-Api-Version", "2022-11-28")
        req.add_header("User-Agent", "dyc-py")
        if data is not None:
            req.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(req, timeout=60) as r:
                raw = r.read()
                return json.loads(raw) if raw else {}
        except urllib.error.HTTPError as e:
            raise RuntimeError("github %s %s -> %d: %s" % (method, path, e.code, e.read().decode()[:300]))

    def user(self):
        return self._req("GET", "/user")

    def repo(self, owner, name):
        return self._req("GET", "/repos/%s/%s" % (owner, name))

    def ensure_fork(self, owner, name, login):
        try:
            self.repo(login, name)
            return
        except RuntimeError:
            pass
        try:
            self._req("POST", "/repos/%s/%s/forks" % (owner, name), {})
        except RuntimeError:
            pass
        for i in range(12):
            time.sleep(2 + i)
            try:
                self.repo(login, name)
                return
            except RuntimeError:
                continue
        raise RuntimeError("fork did not become ready")

    def branch_sha(self, owner, name, branch):
        return self._req("GET", "/repos/%s/%s/git/ref/heads/%s" % (owner, name, branch))["object"]["sha"]

    def base_tree(self, owner, name, sha):
        return self._req("GET", "/repos/%s/%s/git/commits/%s" % (owner, name, sha))["tree"]["sha"]

    def create_branch(self, owner, name, branch, sha):
        self._req("POST", "/repos/%s/%s/git/refs" % (owner, name),
                  {"ref": "refs/heads/" + branch, "sha": sha})

    def create_blob(self, owner, name, content):
        import base64
        return self._req("POST", "/repos/%s/%s/git/blobs" % (owner, name),
                         {"content": base64.b64encode(content).decode(), "encoding": "base64"})["sha"]

    def create_tree(self, owner, name, base, entries):
        return self._req("POST", "/repos/%s/%s/git/trees" % (owner, name),
                         {"base_tree": base, "tree": entries})["sha"]

    def create_commit(self, owner, name, msg, tree, parent):
        return self._req("POST", "/repos/%s/%s/git/commits" % (owner, name),
                         {"message": msg, "tree": tree, "parents": [parent]})["sha"]

    def update_branch(self, owner, name, branch, sha):
        self._req("PATCH", "/repos/%s/%s/git/refs/heads/%s" % (owner, name, branch),
                  {"sha": sha, "force": False})

    def create_pr(self, owner, name, head, base, title, body):
        return self._req("POST", "/repos/%s/%s/pulls" % (owner, name),
                         {"title": title, "head": head, "base": base, "body": body,
                          "maintainer_can_modify": True})["html_url"]


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def _short(s):
    return s[:8] if len(s) > 8 else s


def _match(selectors, s):
    for sel in selectors:
        if sel == "all":
            return True
        if s["session"].startswith(sel):
            return True
        low = sel.lower()
        if low in s["name"].lower() or low in s["cwd"].lower() or low in s["encoded"].lower():
            return True
    return False


def _make_scrubber(sessions):
    return Scrubber(usernames([s["encoded"] for s in sessions]))


def cmd_scan(args):
    show_all = "--all" in args
    as_json = "--json" in args
    roots = config_roots()
    if not roots:
        print("scan: no Claude config roots found (~/.claude/projects, XDG, CLAUDE_CONFIG_DIR)", file=sys.stderr)
        return 1
    sessions = discover_sessions()
    rows, total = [], 0
    for s in sessions:
        main = 0
        try:
            with safe_open(s["main"], s["root"]) as f:
                main = count_fable(f)
        except IOError:
            pass
        sub = 0
        for sf in s["subagents"]:
            try:
                with safe_open(sf, s["root"]) as f:
                    sub += count_fable(f)
            except IOError:
                pass
        if main + sub == 0 and not show_all:
            continue
        total += main + sub
        rows.append({"project": s["name"], "path": home_abbrev(s["cwd"]),
                     "session": _short(s["session"]), "fable_main": main,
                     "fable_subagents": sub,
                     "bytes": os.path.getsize(s["main"]) if os.path.exists(s["main"]) else 0})
    if as_json:
        print(json.dumps({"roots": roots, "sessions": rows, "total_fable_turns": total}, indent=2))
        return 0
    print("Claude config roots:")
    for r in roots:
        print("  " + r)
    print()
    if not rows:
        print("No sessions with genuine Fable 5 turns found. (Use --all to list every session.)")
        return 0
    print("%-20s  %-32s  %-10s  %6s  %8s" % ("PROJECT", "PATH", "SESSION", "FABLE", "SUBAGENT"))
    for r in rows:
        print("%-20s  %-32s  %-10s  %6d  %8d" % (r["project"][:20], r["path"][:32], r["session"],
                                                 r["fable_main"], r["fable_subagents"]))
    print("\n%d session(s) contain genuine Fable 5 turns; %d Fable 5 turn(s) total." % (len(rows), total))
    print("Select projects to donate by name or path (e.g. `python3 dyc.py donate <name>`), or 'all'.")
    return 0


def cmd_preview(args):
    full = "--full" in args
    as_json = "--json" in args
    selectors = [a for a in args if not a.startswith("--")]
    if not selectors:
        print("preview: a selector is required (project name, session id prefix, or 'all')", file=sys.stderr)
        return 2
    sessions = discover_sessions()
    scrub = _make_scrubber(sessions)
    matched = produced = dropped = 0
    for s in sessions:
        if not _match(selectors, s):
            continue
        matched += 1
        for rec, status, reason in build_records(s, scrub, VERSION):
            if status == "ok":
                produced += 1
                if as_json:
                    print(json.dumps(rec, indent=2, ensure_ascii=False))
                else:
                    rs = rec["redaction_summary"]
                    print("● %s/%s  record_id=%s" % (s["name"], _short(s["session"]), rec["record_id"]))
                    print("    messages=%d  models=%s  subagent=%s" %
                          (len(rec["messages"]), ",".join(rec["models_present"]), rec["is_subagent"]))
                    print("    redactions: " + "  ".join("%s=%d" % (k, rs[k]) for k in rs))
                    if full:
                        print(json.dumps(rec, indent=2, ensure_ascii=False))
            elif status == "dropped" and not as_json:
                dropped += 1
                print("[dropped] %s/%s — %s" % (s["name"], _short(s["session"]), reason))
    if matched == 0:
        print("preview: no sessions matched %s" % selectors, file=sys.stderr)
        return 1
    if not as_json:
        print("\nMatched %d session(s): %d record(s) ready, %d dropped (fail-closed)." % (matched, produced, dropped))
        if not full:
            print("Re-run with --full to print the complete scrubbed payload.")
    return 0


def cmd_auth(args):
    if not args:
        print("usage: dyc.py auth login|status|logout", file=sys.stderr)
        return 2
    if args[0] == "login":
        tok = ""
        if "--token-stdin" in args:
            tok = sys.stdin.readline().strip()
        else:
            tok = gh_cli_token()
            if not tok:
                sys.stderr.write("Paste a fine-grained GitHub token (fork + contents:write + pull-requests:write): ")
                sys.stderr.flush()
                tok = sys.stdin.readline().strip()
        if not tok:
            print("auth login: no token provided", file=sys.stderr)
            return 1
        try:
            u = GitHub(tok).user()
        except RuntimeError as e:
            print("auth login: token rejected: %s" % e, file=sys.stderr)
            return 1
        save_token(tok)
        print("Logged in as %s. Token stored (0600)." % u["login"])
        return 0
    if args[0] == "status":
        tok, src = resolve_token()
        if not tok:
            print("Not logged in. Run `python3 dyc.py auth login`.")
            return 0
        try:
            print("Logged in as %s (token source: %s)." % (GitHub(tok).user()["login"], src))
        except RuntimeError as e:
            print("Token present (%s) but rejected: %s" % (src, e))
            return 1
        return 0
    if args[0] == "logout":
        p = os.path.join(state_dir(), "token")
        if os.path.exists(p):
            os.remove(p)
        print("Stored token removed.")
        return 0
    print("auth: unknown subcommand %r" % args[0], file=sys.stderr)
    return 2


def cmd_status(args):
    roots = config_roots()
    _, src = resolve_token()
    print("dyc %s (python)   scrubber %s   schema %s" % (VERSION, scrubber_version(), SCHEMA_VERSION))
    print("token:           %s" % (src or "none (run `python3 dyc.py auth login`)"))
    print("staging target:  %s/%s" % (STAGING_OWNER, STAGING_REPO))
    print("records donated: %d" % len(load_donated()))
    print("claude config roots:")
    for r in roots or ["  (none found)"]:
        print("  " + r)
    return 0


def cmd_donate(args):
    dry = "--dry-run" in args
    yes = "--yes" in args or "-y" in args
    selectors = [a for a in args if not a.startswith("-")]
    if not selectors:
        print("donate: at least one selector is required (project name, session id, or 'all')", file=sys.stderr)
        return 2
    sessions = discover_sessions()
    scrub = _make_scrubber(sessions)
    donated = load_donated()
    items, dropped, skipped = [], 0, 0
    for s in sessions:
        if not _match(selectors, s):
            continue
        for rec, status, reason in build_records(s, scrub, VERSION):
            if status == "ok":
                if rec["record_id"] in donated:
                    skipped += 1
                    continue
                items.append((s, rec))
            elif status == "dropped":
                dropped += 1
    if not items:
        print("Nothing to donate. (%d dropped fail-closed, %d already donated.)" % (dropped, skipped))
        return 0
    print("Ready to donate %d record(s) to %s/%s  (%d dropped fail-closed, %d already donated)" %
          (len(items), STAGING_OWNER, STAGING_REPO, dropped, skipped))
    for s, rec in items:
        print("  %s  %s/%s  (%d messages)" % (rec["record_id"][:12], s["name"], _short(s["session"]), len(rec["messages"])))
    print("\nLicense: CC0-1.0   Provenance: self-attested   DCO sign-off added to the commit.")
    print("Tip: `python3 dyc.py preview <name> --full` to inspect the exact scrubbed payload first.")
    if dry:
        print("\n[dry-run] No network calls were made. The above would be submitted as one PR.")
        return 0
    if not yes:
        sys.stderr.write("Submit %d record(s) as a public CC0 PR? [y/N] " % len(items))
        sys.stderr.flush()
        if sys.stdin.readline().strip().lower() not in ("y", "yes"):
            print("Aborted. Nothing was sent.")
            return 0
    token, src = resolve_token()
    if not token:
        print("donate: no GitHub token. Run `python3 dyc.py auth login`.", file=sys.stderr)
        return 1
    gh = GitHub(token)
    try:
        user = gh.user()
        login = user["login"]
        base = gh.repo(STAGING_OWNER, STAGING_REPO)
        gh.ensure_fork(STAGING_OWNER, STAGING_REPO, login)
        import binascii
        branch = "dyc/" + binascii.hexlify(os.urandom(6)).decode()
        base_sha = gh.branch_sha(login, STAGING_REPO, base["default_branch"])
        gh.create_branch(login, STAGING_REPO, branch, base_sha)
        base_tree = gh.base_tree(login, STAGING_REPO, base_sha)
        entries = []
        for s, rec in items:
            rec["contributor"] = login
            rec["dco"] = True
            blob = gh.create_blob(login, STAGING_REPO, json.dumps(rec, indent=2, ensure_ascii=False).encode())
            entries.append({"path": shard_path(rec["record_id"]), "mode": "100644", "type": "blob", "sha": blob})
        tree = gh.create_tree(login, STAGING_REPO, base_tree, entries)
        name = user.get("name") or login
        email = user.get("email") or (login + "@users.noreply.github.com")
        msg = "Donate %d Fable 5 record(s)\n\nSigned-off-by: %s <%s>\n" % (len(items), name, email)
        commit = gh.create_commit(login, STAGING_REPO, msg, tree, base_sha)
        gh.update_branch(login, STAGING_REPO, branch, commit)
        body = ("Automated donation of %d Claude Fable 5 record(s) via dyc.\n\n"
                "- License: CC0-1.0\n- Provenance: self-attested, unverified\n"
                "- Files touched: only staging/** content-addressed records\n- DCO: signed off\n") % len(items)
        url = gh.create_pr(STAGING_OWNER, STAGING_REPO, login + ":" + branch, base["default_branch"],
                           "Donate %d Fable 5 record(s)" % len(items), body)
    except RuntimeError as e:
        print("donate: %s" % e, file=sys.stderr)
        return 1
    now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    for s, rec in items:
        append_donated({"record_id": rec["record_id"], "pr_url": url, "status": "submitted", "at": now})
    print("\n✅ Submitted PR: %s" % url)
    print("The CI gate will validate and the bot will merge it. Thank you for contributing!")
    return 0


def cmd_build(args):
    """Write the cleaned-up records for the picked projects to a local folder.
    NO network calls — just reads transcripts and writes JSON files you can then
    open a PR with (using git/gh yourself)."""
    out = "out"
    selectors = []
    i = 0
    while i < len(args):
        a = args[i]
        if a == "--out":
            if i + 1 >= len(args):
                print("build: --out needs a folder", file=sys.stderr)
                return 2
            i += 1
            out = args[i]
        elif a.startswith("-"):
            print("build: unknown flag %r" % a, file=sys.stderr)
            return 2
        else:
            selectors.append(a)
        i += 1
    if not selectors:
        print("build: at least one selector is required (project name, or 'all')", file=sys.stderr)
        return 2
    sessions = discover_sessions()
    scrub = _make_scrubber(sessions)
    n = 0
    for s in sessions:
        if not _match(selectors, s):
            continue
        for rec, status, reason in build_records(s, scrub, VERSION):
            if status != "ok":
                continue
            rec["dco"] = True
            rid = rec["record_id"]
            d = os.path.join(out, "staging", rid[:2], rid[2:4])
            os.makedirs(d, exist_ok=True)
            with open(os.path.join(d, rid + ".json"), "w") as f:
                json.dump(rec, f, indent=2, ensure_ascii=False)
            n += 1
    if n == 0:
        print("Nothing to build (no Fable 5 records matched, or all were dropped).")
        return 0
    print("Wrote %d record(s) under %s/staging/  — no network calls were made." % (n, out))
    print("Next: open a PR adding %s/staging/** to GeeveGeorge/donate-your-code-staging (use git/gh)." % out)
    return 0


USAGE = """dyc — Donate Your Code (python client)

  python3 dyc.py scan [--all] [--json]
  python3 dyc.py preview <selector> [more...] [--full] [--json]
  python3 dyc.py build <selector> [more...] [--out FOLDER]    # write records locally, NO network
  python3 dyc.py auth login|status|logout
  python3 dyc.py donate <selector> [more...] [--dry-run] [--yes]
  python3 dyc.py status

A selector is a project name substring, a session id prefix, or 'all'.
scan/preview/build/donate --dry-run make NO network calls.
"""


def main(argv):
    if not argv:
        sys.stderr.write(USAGE)
        return 2
    cmd, rest = argv[0], argv[1:]
    if cmd == "scan":
        return cmd_scan(rest)
    if cmd == "preview":
        return cmd_preview(rest)
    if cmd == "build":
        return cmd_build(rest)
    if cmd == "auth":
        return cmd_auth(rest)
    if cmd == "donate":
        return cmd_donate(rest)
    if cmd == "status":
        return cmd_status(rest)
    if cmd in ("version", "--version", "-v"):
        print("dyc %s (python)\nscrubber %s\nrecord-schema %s" % (VERSION, scrubber_version(), SCHEMA_VERSION))
        return 0
    if cmd in ("help", "--help", "-h"):
        sys.stderr.write(USAGE)
        return 0
    sys.stderr.write("dyc: unknown command %r\n" % cmd)
    sys.stderr.write(USAGE)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
