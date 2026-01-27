#!/usr/bin/env python3
import base64
import argparse
import bisect
import dataclasses
import http.client
import json
import math
import os
import platform
import random
import ssl
import subprocess
import sys
import tempfile
import threading
import time
import urllib.parse
from collections import Counter
from typing import Any, Optional


COMPLEX_PYTHON_HANDLER_CODE = r"""import hashlib
import json
import math
import re
import zlib


def _xorshift32(x: int) -> int:
    x &= 0xFFFFFFFF
    x ^= (x << 13) & 0xFFFFFFFF
    x ^= (x >> 17) & 0xFFFFFFFF
    x ^= (x << 5) & 0xFFFFFFFF
    return x & 0xFFFFFFFF


def handler(event):
    # Tunables (kept within sane bounds to avoid OOM/timeouts by default)
    n = int(event.get("n", 2500))
    loops = int(event.get("loops", 15))
    payload_kb = int(event.get("payload_kb", 16))
    seed = int(event.get("seed", 1)) & 0xFFFFFFFF

    if n < 0:
        n = 0
    if n > 200_000:
        n = 200_000
    if loops < 0:
        loops = 0
    if loops > 2_000:
        loops = 2_000
    if payload_kb < 0:
        payload_kb = 0
    if payload_kb > 4_096:
        payload_kb = 4_096

    # Pseudo-random, deterministic workload
    x = seed or 1
    nums = []
    for _ in range(n):
        x = _xorshift32(x)
        nums.append(x)

    nums.sort()

    # Mix of integer ops + a bit of trig
    total = 0
    trig = 0.0
    step = max(1, n // 1024)
    for i, v in enumerate(nums):
        total += v
        if i % step == 0:
            t = float(v % 10_000)
            trig += math.sin(t) * math.cos(t / 3.0)

    total64 = total & 0xFFFFFFFFFFFFFFFF

    # Build a somewhat large payload and run compress/decompress
    base = json.dumps({"n": n, "seed": seed, "total64": total64, "trig": trig}).encode("utf-8")
    target_len = payload_kb * 1024
    if target_len > 0:
        blob = (base * ((target_len // max(1, len(base))) + 1))[:target_len]
    else:
        blob = base

    comp = zlib.compress(blob, level=6)
    decomp = zlib.decompress(comp)

    # Hash chaining to burn CPU in a controllable way
    h = hashlib.sha256(decomp).digest()
    for i in range(loops):
        h = hashlib.sha256(h + (i & 0xFFFFFFFF).to_bytes(4, "little")).digest()
    digest = h.hex()

    # Regex scan on a small slice (keeps it deterministic but non-trivial)
    text = decomp[:512].decode("utf-8", errors="ignore")
    regex_hits = len(re.findall(r"[0-9a-f]{4,}", digest + text))

    return {
        "ok": True,
        "n": n,
        "loops": loops,
        "payload_kb": payload_kb,
        "seed": seed,
        "total64": total64,
        "trig": trig,
        "digest": digest,
        "regex_hits": regex_hits,
    }
"""

COMPLEX_NODE_HANDLER_CODE = r"""const crypto = require("crypto");
const zlib = require("zlib");

function xorshift32(x) {
  x = x >>> 0;
  x ^= (x << 13) >>> 0;
  x ^= (x >>> 17) >>> 0;
  x ^= (x << 5) >>> 0;
  return x >>> 0;
}

module.exports.handler = async (event) => {
  let n = Number(event.n ?? 2500);
  let loops = Number(event.loops ?? 15);
  let payloadKb = Number(event.payload_kb ?? 16);
  let seed = Number(event.seed ?? 1) >>> 0;

  if (!Number.isFinite(n) || n < 0) n = 0;
  if (n > 200000) n = 200000;
  if (!Number.isFinite(loops) || loops < 0) loops = 0;
  if (loops > 2000) loops = 2000;
  if (!Number.isFinite(payloadKb) || payloadKb < 0) payloadKb = 0;
  if (payloadKb > 4096) payloadKb = 4096;
  if (seed === 0) seed = 1;

  let x = seed;
  const nums = new Array(n);
  for (let i = 0; i < n; i++) {
    x = xorshift32(x);
    nums[i] = x;
  }
  nums.sort((a, b) => a - b);

  let total = 0n;
  let trig = 0.0;
  const step = Math.max(1, Math.floor(n / 1024));
  for (let i = 0; i < nums.length; i++) {
    total += BigInt(nums[i] >>> 0);
    if (i % step === 0) {
      const t = Number(nums[i] % 10000);
      trig += Math.sin(t) * Math.cos(t / 3.0);
    }
  }
  const total64 = Number(total & 0xffffffffffffffffn);

  const base = Buffer.from(JSON.stringify({ n, seed, total64, trig }), "utf8");
  const targetLen = payloadKb * 1024;
  let blob = base;
  if (targetLen > 0) {
    blob = Buffer.allocUnsafe(targetLen);
    for (let off = 0; off < targetLen; ) {
      const ncopy = Math.min(base.length, targetLen - off);
      base.copy(blob, off, 0, ncopy);
      off += ncopy;
    }
  }

  const comp = zlib.deflateSync(blob, { level: 6 });
  const decomp = zlib.inflateSync(comp);

  let h = crypto.createHash("sha256").update(decomp).digest();
  for (let i = 0; i < loops; i++) {
    const b = Buffer.allocUnsafe(4);
    b.writeUInt32LE(i >>> 0, 0);
    h = crypto.createHash("sha256").update(Buffer.concat([h, b])).digest();
  }
  const digest = h.toString("hex");

  const text = decomp.subarray(0, 512).toString("utf8");
  const hits = (digest + text).match(/[0-9a-f]{4,}/g);
  const regexHits = hits ? hits.length : 0;

  return {
    ok: true,
    n,
    loops,
    payload_kb: payloadKb,
    seed,
    total64,
    trig,
    digest,
    regex_hits: regexHits,
  };
};
"""

COMPLEX_GO_STDIN_HANDLER_SOURCE = r"""package main

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"sort"
)

type Input struct {
	N         int   `json:"n"`
	Loops     int   `json:"loops"`
	PayloadKB int   `json:"payload_kb"`
	Seed      uint32 `json:"seed"`
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func xorshift32(x uint32) uint32 {
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	return x
}

var re = regexp.MustCompile(`[0-9a-f]{4,}`)

func main() {
	raw, _ := io.ReadAll(os.Stdin)
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}

	var in Input
	_ = json.Unmarshal(raw, &in)

	if in.N == 0 {
		in.N = 2500
	}
	if in.Loops == 0 {
		in.Loops = 15
	}
	if in.PayloadKB == 0 {
		in.PayloadKB = 16
	}
	if in.Seed == 0 {
		in.Seed = 1
	}

	in.N = clampInt(in.N, 0, 200000)
	in.Loops = clampInt(in.Loops, 0, 2000)
	in.PayloadKB = clampInt(in.PayloadKB, 0, 4096)

	x := in.Seed
	nums := make([]uint32, in.N)
	for i := 0; i < in.N; i++ {
		x = xorshift32(x)
		nums[i] = x
	}
	sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })

	var total uint64
	var trig float64
	step := 1
	if in.N > 0 {
		step = in.N / 1024
		if step < 1 {
			step = 1
		}
	}
	for i, v := range nums {
		total += uint64(v)
		if i%step == 0 {
			t := float64(v % 10000)
			trig += math.Sin(t) * math.Cos(t/3.0)
		}
	}

	base := []byte(fmt.Sprintf(`{"n":%d,"seed":%d,"total64":%d,"trig":%.8f}`, in.N, in.Seed, total, trig))
	targetLen := in.PayloadKB * 1024
	var blob []byte
	if targetLen > 0 {
		blob = make([]byte, 0, targetLen)
		for len(blob) < targetLen {
			need := targetLen - len(blob)
			if need >= len(base) {
				blob = append(blob, base...)
			} else {
				blob = append(blob, base[:need]...)
			}
		}
	} else {
		blob = base
	}

	var comp bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&comp, 6)
	_, _ = zw.Write(blob)
	_ = zw.Close()

	zr, _ := zlib.NewReader(bytes.NewReader(comp.Bytes()))
	decomp, _ := io.ReadAll(zr)
	_ = zr.Close()

	h := sha256.Sum256(decomp)
	for i := 0; i < in.Loops; i++ {
		var b [4]byte
		u := uint32(i)
		b[0] = byte(u)
		b[1] = byte(u >> 8)
		b[2] = byte(u >> 16)
		b[3] = byte(u >> 24)
		buf := make([]byte, 0, 32+4)
		buf = append(buf, h[:]...)
		buf = append(buf, b[:]...)
		h = sha256.Sum256(buf)
	}
	digest := hex.EncodeToString(h[:])

	text := string(decomp[:minInt(512, len(decomp))])
	regexHits := len(re.FindAllStringIndex(digest+text, -1))

	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"ok":         true,
		"n":          in.N,
		"loops":      in.Loops,
		"payload_kb": in.PayloadKB,
		"seed":       in.Seed,
		"total64":    total,
		"trig":       trig,
		"digest":     digest,
		"regex_hits": regexHits,
	})
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
"""

COMPLEX_RUST_WASM_HANDLER_SOURCE = r"""#[no_mangle]
pub extern "C" fn alloc(size: usize) -> *mut u8 {
    let mut buf = Vec::<u8>::with_capacity(size);
    let ptr = buf.as_mut_ptr();
    std::mem::forget(buf);
    ptr
}

fn find_num(input: &[u8], key: &[u8]) -> Option<i64> {
    let mut i = 0usize;
    while i + key.len() <= input.len() {
        if &input[i..i + key.len()] == key {
            let mut j = i + key.len();
            let mut sign: i64 = 1;
            if j < input.len() && input[j] == b'-' {
                sign = -1;
                j += 1;
            }
            let mut v: i64 = 0;
            let mut any = false;
            while j < input.len() {
                let c = input[j];
                if c < b'0' || c > b'9' {
                    break;
                }
                any = true;
                v = v.saturating_mul(10).saturating_add((c - b'0') as i64);
                j += 1;
            }
            if any {
                return Some(v * sign);
            }
            return None;
        }
        i += 1;
    }
    None
}

fn clamp_i64(v: i64, lo: i64, hi: i64) -> i64 {
    if v < lo {
        return lo;
    }
    if v > hi {
        return hi;
    }
    v
}

fn xorshift32(mut x: u32) -> u32 {
    x ^= x << 13;
    x ^= x >> 17;
    x ^= x << 5;
    x
}

#[no_mangle]
pub extern "C" fn handle(ptr: u32, len: u32) -> u64 {
    let input = unsafe { std::slice::from_raw_parts(ptr as *const u8, len as usize) };

    let n = clamp_i64(find_num(input, b"\\\"n\\\":").unwrap_or(2500), 0, 200000) as usize;
    let loops = clamp_i64(find_num(input, b"\\\"loops\\\":").unwrap_or(15), 0, 2000) as usize;
    let payload_kb = clamp_i64(find_num(input, b"\\\"payload_kb\\\":").unwrap_or(16), 0, 1024) as usize;
    let mut seed = find_num(input, b"\\\"seed\\\":").unwrap_or(1) as u32;
    if seed == 0 {
        seed = 1;
    }

    let mut x = seed;
    let mut nums: Vec<u32> = Vec::with_capacity(n);
    for _ in 0..n {
        x = xorshift32(x);
        nums.push(x);
    }
    nums.sort_unstable();

    let mut acc: u64 = 0;
    for v in &nums {
        acc = acc.wrapping_add(*v as u64);
    }

    let target_len = payload_kb * 1024;
    let mut blob: Vec<u8> = Vec::with_capacity(target_len.max(1));
    if target_len > 0 {
        while blob.len() < target_len {
            let need = target_len - blob.len();
            let take = if need >= input.len() { input.len() } else { need };
            blob.extend_from_slice(&input[..take]);
            if input.is_empty() {
                break;
            }
        }
    } else {
        blob.extend_from_slice(input);
    }

    let mut h: u64 = 1469598103934665603u64;
    for b in &blob {
        h ^= *b as u64;
        h = h.wrapping_mul(1099511628211u64);
    }
    for i in 0..loops {
        h ^= (i as u64).wrapping_mul(0x9e3779b97f4a7c15u64);
        h = h.rotate_left(13) ^ (acc.wrapping_add(h));
        h = h.wrapping_mul(0xbf58476d1ce4e5b9u64);
    }

    let out = format!(
        r#"{{"ok":true,"runtime":"wasm","n":{},"loops":{},"payload_kb":{},"seed":{},"acc":{},"hash":"{:016x}"}}"#,
        n, loops, payload_kb, seed, acc, h
    );
    let mut out_bytes = out.into_bytes();
    let out_ptr = out_bytes.as_ptr() as u32;
    let out_len = out_bytes.len() as u32;
    std::mem::forget(out_bytes);
    ((out_ptr as u64) << 32) | (out_len as u64)
}
"""


def _pct(sorted_values: list[float], p: float) -> Optional[float]:
    if not sorted_values:
        return None
    if p <= 0:
        return sorted_values[0]
    if p >= 100:
        return sorted_values[-1]
    n = len(sorted_values)
    k = (p / 100.0) * (n - 1)
    f = int(math.floor(k))
    c = int(math.ceil(k))
    if f == c:
        return sorted_values[f]
    d0 = sorted_values[f] * (c - k)
    d1 = sorted_values[c] * (k - f)
    return d0 + d1


@dataclasses.dataclass
class BaseURL:
    scheme: str
    host: str
    port: int
    path_prefix: str


def parse_base_url(base_url: str) -> BaseURL:
    parsed = urllib.parse.urlparse(base_url)
    if parsed.scheme not in ("http", "https"):
        raise ValueError(f"unsupported base url scheme: {parsed.scheme!r}")
    if not parsed.hostname:
        raise ValueError("base url must include hostname")
    port = parsed.port
    if port is None:
        port = 443 if parsed.scheme == "https" else 80
    path_prefix = parsed.path.rstrip("/")
    return BaseURL(scheme=parsed.scheme, host=parsed.hostname, port=port, path_prefix=path_prefix)


class JSONHTTPClient:
    def __init__(self, base: BaseURL, timeout: float, insecure_tls: bool):
        self._base = base
        self._timeout = timeout
        self._insecure_tls = insecure_tls
        self._conn: Optional[http.client.HTTPConnection] = None

    def close(self) -> None:
        if self._conn is not None:
            try:
                self._conn.close()
            finally:
                self._conn = None

    def _connect(self) -> http.client.HTTPConnection:
        if self._conn is not None:
            return self._conn

        if self._base.scheme == "https":
            ctx = ssl.create_default_context()
            if self._insecure_tls:
                ctx.check_hostname = False
                ctx.verify_mode = ssl.CERT_NONE
            self._conn = http.client.HTTPSConnection(
                self._base.host, self._base.port, timeout=self._timeout, context=ctx
            )
        else:
            self._conn = http.client.HTTPConnection(self._base.host, self._base.port, timeout=self._timeout)
        return self._conn

    def request_json(self, method: str, path: str, body: Any | None) -> tuple[int, dict[str, str], Any]:
        full_path = f"{self._base.path_prefix}{path}"
        headers = {"Accept": "application/json"}
        body_bytes: Optional[bytes]
        if body is None:
            body_bytes = None
        else:
            body_bytes = json.dumps(body, separators=(",", ":"), ensure_ascii=False).encode("utf-8")
            headers["Content-Type"] = "application/json"

        last_exc: Optional[BaseException] = None
        for attempt in range(2):
            try:
                conn = self._connect()
                conn.request(method, full_path, body=body_bytes, headers=headers)
                resp = conn.getresponse()
                resp_body = resp.read()
                resp_headers = {k.lower(): v for k, v in resp.getheaders()}
                parsed = json.loads(resp_body.decode("utf-8")) if resp_body else None
                return resp.status, resp_headers, parsed
            except (
                BrokenPipeError,
                ConnectionResetError,
                http.client.CannotSendRequest,
                http.client.ResponseNotReady,
                http.client.RemoteDisconnected,
                TimeoutError,
            ) as exc:
                last_exc = exc
                self.close()
                if attempt == 0:
                    continue
                raise
            except json.JSONDecodeError as exc:
                last_exc = exc
                raise RuntimeError("non-json response received") from exc
            except BaseException as exc:
                last_exc = exc
                raise

        raise RuntimeError("request failed") from last_exc


def _run(cmd: list[str], *, cwd: str | None = None, env: dict[str, str] | None = None) -> str:
    proc = subprocess.run(
        cmd,
        cwd=cwd,
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    if proc.returncode != 0:
        raise RuntimeError(f"command failed ({proc.returncode}): {' '.join(cmd)}\n{proc.stderr.strip()}")
    return proc.stdout


def docker_server_arch() -> str:
    try:
        out = _run(["docker", "version", "--format", "{{.Server.Os}}/{{.Server.Arch}}"]).strip()
        if "/" in out:
            os_name, arch = out.split("/", 1)
            if os_name == "linux" and arch:
                return arch
    except BaseException:
        pass

    machine = platform.machine().lower()
    if machine in ("arm64", "aarch64"):
        return "arm64"
    if machine in ("x86_64", "amd64"):
        return "amd64"
    return "amd64"


def read_file_b64(path: str) -> str:
    with open(path, "rb") as f:
        return base64.b64encode(f.read()).decode("ascii")


def build_go_binary_b64(*, arch: str, artifact_dir: str, source: str) -> str:
    os.makedirs(artifact_dir, exist_ok=True)
    cache_path = os.path.join(artifact_dir, f"stress_go_linux_{arch}.b64")
    if os.path.exists(cache_path):
        return open(cache_path, "r", encoding="utf-8").read().strip()

    with tempfile.TemporaryDirectory(prefix="function-stress-go-") as td:
        src_path = os.path.join(td, "main.go")
        bin_path = os.path.join(td, "handler")
        with open(src_path, "w", encoding="utf-8") as f:
            f.write(source)

        env = dict(os.environ)
        env["CGO_ENABLED"] = "0"
        env["GOOS"] = "linux"
        env["GOARCH"] = arch
        _run(["go", "build", "-o", bin_path, src_path], env=env)

        b64 = read_file_b64(bin_path)
        with open(cache_path, "w", encoding="utf-8") as f:
            f.write(b64)
        return b64


def build_wasm_b64(*, artifact_dir: str, source: str) -> str:
    os.makedirs(artifact_dir, exist_ok=True)
    cache_path = os.path.join(artifact_dir, "stress_wasm.b64")
    if os.path.exists(cache_path):
        return open(cache_path, "r", encoding="utf-8").read().strip()

    with tempfile.TemporaryDirectory(prefix="function-stress-wasm-") as td:
        src_path = os.path.join(td, "handler.rs")
        wasm_path = os.path.join(td, "handler.wasm")
        with open(src_path, "w", encoding="utf-8") as f:
            f.write(source)

        _run(
            [
                "docker",
                "run",
                "--rm",
                "-v",
                f"{td}:/work",
                "-w",
                "/work",
                "rust:1.75",
                "bash",
                "-c",
                "set -e; rustup target add wasm32-unknown-unknown >/dev/null; "
                "rustc --target wasm32-unknown-unknown -O -C panic=abort --crate-type=cdylib handler.rs -o handler.wasm",
            ]
        )

        b64 = read_file_b64(wasm_path)
        with open(cache_path, "w", encoding="utf-8") as f:
            f.write(b64)
        return b64


def ensure_function(
    client: JSONHTTPClient,
    *,
    name: str,
    runtime: str,
    handler: str,
    code: str,
    description: str,
    memory_mb: int,
    timeout_sec: int,
    env_vars: dict[str, str],
    force_recreate: bool,
) -> None:
    fn_path = f"/api/v1/functions/{urllib.parse.quote(name)}"
    status, _, fn = client.request_json("GET", fn_path, None)
    if status == 404:
        create_req = {
            "name": name,
            "runtime": runtime,
            "handler": handler,
            "code": code,
            "description": description,
            "memory_mb": memory_mb,
            "timeout_sec": timeout_sec,
            "env_vars": env_vars,
        }
        c_status, _, c_body = client.request_json("POST", "/api/v1/functions", create_req)
        if c_status not in (200, 201):
            raise RuntimeError(f"failed to create function: HTTP {c_status}: {c_body}")
        return
    if status != 200:
        raise RuntimeError(f"failed to query function: HTTP {status}: {fn}")

    if isinstance(fn, dict):
        existing_runtime = fn.get("runtime")
        if force_recreate and existing_runtime and existing_runtime != runtime:
            d_status, _, d_body = client.request_json("DELETE", fn_path, None)
            if d_status not in (200, 202, 204):
                raise RuntimeError(f"failed to delete function for recreate: HTTP {d_status}: {d_body}")
            create_req = {
                "name": name,
                "runtime": runtime,
                "handler": handler,
                "code": code,
                "description": description,
                "memory_mb": memory_mb,
                "timeout_sec": timeout_sec,
                "env_vars": env_vars,
            }
            c_status, _, c_body = client.request_json("POST", "/api/v1/functions", create_req)
            if c_status not in (200, 201):
                raise RuntimeError(f"failed to recreate function: HTTP {c_status}: {c_body}")
            return

    update_req = {
        "code": code,
        "handler": handler,
        "description": description,
        "memory_mb": memory_mb,
        "timeout_sec": timeout_sec,
        "env_vars": env_vars,
    }
    u_status, _, u_body = client.request_json("PUT", fn_path, update_req)
    if u_status != 200:
        raise RuntimeError(f"failed to update function: HTTP {u_status}: {u_body}")


def get_function(client: JSONHTTPClient, name: str) -> Optional[dict[str, Any]]:
    fn_path = f"/api/v1/functions/{urllib.parse.quote(name)}"
    status, _, body = client.request_json("GET", fn_path, None)
    if status == 404:
        return None
    if status != 200 or not isinstance(body, dict):
        raise RuntimeError(f"failed to get function: HTTP {status}: {body}")
    return body


@dataclasses.dataclass(frozen=True)
class TargetSpec:
    name: str
    weight: int
    runtime: Optional[str]


@dataclasses.dataclass(frozen=True)
class Target:
    name: str
    weight: int
    path: str
    runtime: Optional[str]


def parse_target_spec(spec: str) -> TargetSpec:
    s = spec.strip()
    if not s:
        raise ValueError("empty target spec")

    weight = 1
    name_part = s
    if ":" in s:
        name_part, w = s.rsplit(":", 1)
        if w:
            weight = int(w)

    runtime: Optional[str] = None
    name = name_part
    if "@" in name_part:
        name, runtime = name_part.split("@", 1)

    name = name.strip()
    if not name:
        raise ValueError(f"invalid target spec: {spec!r}")
    if weight <= 0:
        raise ValueError(f"invalid target weight: {spec!r}")

    return TargetSpec(name=name, weight=weight, runtime=runtime)


def make_targets(specs: list[TargetSpec]) -> list[Target]:
    targets: list[Target] = []
    for s in specs:
        targets.append(
            Target(
                name=s.name,
                weight=s.weight,
                path=f"/api/v1/functions/{urllib.parse.quote(s.name)}/invoke",
                runtime=s.runtime,
            )
        )
    return targets


@dataclasses.dataclass
class WorkerResult:
    requests: int
    ok: int
    statuses: Counter[int]
    cold_starts: int
    latencies_ms: list[float]
    error_samples: list[str]
    by_target: dict[str, "WorkerResult"]


def run_worker(
    *,
    worker_id: int,
    base: BaseURL,
    targets: list[Target],
    payload: dict[str, Any],
    auto_fields: bool,
    timeout: float,
    insecure_tls: bool,
    start_at: float,
    ramp_delay_s: float,
    end_at: Optional[float],
    next_request_id: list[int],
    next_lock: threading.Lock,
    max_requests: Optional[int],
    qps_per_worker: Optional[float],
    ok_min: int,
    ok_max: int,
    max_error_samples: int,
) -> WorkerResult:
    client = JSONHTTPClient(base, timeout=timeout, insecure_tls=insecure_tls)
    statuses: Counter[int] = Counter()
    latencies_ms: list[float] = []
    error_samples: list[str] = []
    cold_starts = 0
    ok = 0
    total = 0

    t0 = start_at + ramp_delay_s
    now = time.perf_counter()
    if now < t0:
        time.sleep(t0 - now)

    cum_weights: list[int] = []
    total_weight = 0
    for t in targets:
        total_weight += t.weight
        cum_weights.append(total_weight)

    pace_next: Optional[float] = None
    pace_interval: Optional[float] = None
    if qps_per_worker and qps_per_worker > 0:
        pace_interval = 1.0 / qps_per_worker
        pace_next = time.perf_counter()

    rnd = random.Random(worker_id)
    by_target: dict[str, WorkerResult] = {}

    try:
        while True:
            if end_at is not None and time.perf_counter() >= end_at:
                break

            req_id: int
            with next_lock:
                req_id = next_request_id[0]
                next_request_id[0] += 1
            if max_requests is not None and req_id >= max_requests:
                break

            if pace_next is not None and pace_interval is not None:
                now = time.perf_counter()
                if now < pace_next:
                    time.sleep(pace_next - now)
                pace_next = max(pace_next + pace_interval, time.perf_counter())

            pick = rnd.uniform(0.0, float(total_weight))
            idx = bisect.bisect_right(cum_weights, pick)
            if idx >= len(targets):
                idx = len(targets) - 1
            target = targets[idx]

            body = dict(payload)
            if auto_fields:
                body["seq"] = req_id
                body["seed"] = (body.get("seed", 1) + req_id) & 0xFFFFFFFF

            started = time.perf_counter()
            try:
                status, _, resp = client.request_json("POST", target.path, body)
                elapsed_ms = (time.perf_counter() - started) * 1000.0
                latencies_ms.append(elapsed_ms)
                statuses[status] += 1
                total += 1

                if ok_min <= status <= ok_max:
                    ok += 1
                else:
                    if len(error_samples) < max_error_samples:
                        error_samples.append(f"HTTP {status}: {resp}")

                if isinstance(resp, dict) and resp.get("cold_start") is True:
                    cold_starts += 1

                ts = by_target.get(target.name)
                if ts is None:
                    ts = WorkerResult(
                        requests=0,
                        ok=0,
                        statuses=Counter(),
                        cold_starts=0,
                        latencies_ms=[],
                        error_samples=[],
                        by_target={},
                    )
                    by_target[target.name] = ts

                ts.latencies_ms.append(elapsed_ms)
                ts.statuses[status] += 1
                ts.requests += 1
                if ok_min <= status <= ok_max:
                    ts.ok += 1
                if isinstance(resp, dict) and resp.get("cold_start") is True:
                    ts.cold_starts += 1
            except BaseException as exc:
                elapsed_ms = (time.perf_counter() - started) * 1000.0
                latencies_ms.append(elapsed_ms)
                statuses[-1] += 1
                total += 1
                if len(error_samples) < max_error_samples:
                    error_samples.append(f"{target.name}: EXC {type(exc).__name__}: {exc}")
    finally:
        client.close()

    return WorkerResult(
        requests=total,
        ok=ok,
        statuses=statuses,
        cold_starts=cold_starts,
        latencies_ms=latencies_ms,
        error_samples=error_samples,
        by_target=by_target,
    )


def main() -> int:
    parser = argparse.ArgumentParser(description="Load/stress test for Function Gateway (stdlib-only).")
    parser.add_argument(
        "--base-url",
        default=os.environ.get("FN_API_URL") or "http://localhost:18080",
        help="Gateway base URL (default: env FN_API_URL or http://localhost:18080)",
    )
    parser.add_argument("--function", default="stress_py", help="Function name to invoke")
    parser.add_argument(
        "--mix",
        action="store_true",
        help="Mixed load test across multiple functions/runtimes (use --target or built-in defaults)",
    )
    parser.add_argument(
        "--target",
        action="append",
        default=[],
        help="Target spec: name[@runtime][:weight] (repeatable). Example: stress_py@python3.11:4",
    )
    parser.add_argument(
        "--artifact-dir",
        default="/tmp/function-stress-artifacts",
        help="Artifact cache dir for auto-building Go/Wasm code during --deploy",
    )
    parser.add_argument("--concurrency", type=int, default=20, help="Number of concurrent worker threads")
    parser.add_argument("--requests", type=int, default=2000, help="Total requests (set 0 to use --duration)")
    parser.add_argument("--duration", type=float, default=0.0, help="Run duration seconds (overrides --requests if > 0)")
    parser.add_argument("--timeout", type=float, default=15.0, help="HTTP timeout seconds")
    parser.add_argument("--warmup", type=int, default=20, help="Warmup request count (serial)")
    parser.add_argument("--ramp-up", type=float, default=0.0, help="Ramp up workers over N seconds")
    parser.add_argument("--qps", type=float, default=0.0, help="Target QPS across all workers (0 = unlimited)")
    parser.add_argument("--ok-min", type=int, default=200, help="Min HTTP status treated as OK")
    parser.add_argument("--ok-max", type=int, default=299, help="Max HTTP status treated as OK")
    parser.add_argument("--max-error-samples", type=int, default=10, help="Max error samples to print")
    parser.add_argument("--json-out", default="", help="Write final report JSON to this path")

    parser.add_argument("--deploy", action="store_true", help="Create/update the target function before running")
    parser.add_argument("--runtime", default="python3.11", help="Runtime for --deploy (default: python3.11)")
    parser.add_argument("--handler", default="handler", help="Handler for --deploy (default: handler)")
    parser.add_argument("--description", default="Stress test function (auto-generated)", help="Description for --deploy")
    parser.add_argument("--memory-mb", type=int, default=256, help="MemoryMB for --deploy")
    parser.add_argument("--timeout-sec", type=int, default=30, help="TimeoutSec for --deploy")
    parser.add_argument("--force-recreate", action="store_true", help="Delete/recreate if runtime mismatches")
    parser.add_argument("--code-file", default="", help="Code file for --deploy (defaults to built-in complex handler)")
    parser.add_argument("--insecure-tls", action="store_true", help="Skip TLS verification (https only)")

    parser.add_argument("--payload", default="", help="Invoke payload as JSON string (overrides payload flags)")
    parser.add_argument(
        "--no-auto-fields",
        action="store_true",
        help="Do not auto-add per-request fields (seq/seed) to the payload",
    )
    parser.add_argument("--n", type=int, default=2500, help="Payload: n (see built-in handler)")
    parser.add_argument("--loops", type=int, default=15, help="Payload: loops (see built-in handler)")
    parser.add_argument("--payload-kb", type=int, default=16, help="Payload: payload_kb (see built-in handler)")
    parser.add_argument("--seed", type=int, default=1, help="Payload: seed (will be incremented per request)")

    args = parser.parse_args()

    if args.concurrency <= 0:
        raise SystemExit("--concurrency must be > 0")

    base = parse_base_url(args.base_url)
    admin = JSONHTTPClient(base, timeout=args.timeout, insecure_tls=args.insecure_tls)

    default_runtimes = {
        "stress_py": "python3.11",
        "stress_node": "nodejs20",
        "stress_go": "go1.24",
        "stress_wasm": "wasm",
    }

    is_mixed = bool(args.target) or bool(args.mix)
    if is_mixed and args.code_file:
        raise SystemExit("--code-file is only supported in single-function mode (no --mix/--target)")

    if is_mixed:
        if args.target:
            specs = [parse_target_spec(s) for s in args.target]
        else:
            specs = [
                TargetSpec(name="stress_py", weight=4, runtime="python3.11"),
                TargetSpec(name="stress_node", weight=3, runtime="nodejs20"),
                TargetSpec(name="stress_go", weight=2, runtime="go1.24"),
                TargetSpec(name="stress_wasm", weight=1, runtime="wasm"),
            ]
    else:
        specs = [TargetSpec(name=args.function, weight=1, runtime=args.runtime)]

    resolved_specs: list[TargetSpec] = []
    for s in specs:
        runtime = s.runtime or default_runtimes.get(s.name)
        if args.deploy and runtime is None:
            raise SystemExit(f"target {s.name!r} missing runtime; use --target {s.name}@python3.11:1")
        resolved_specs.append(TargetSpec(name=s.name, weight=s.weight, runtime=runtime))
    specs = resolved_specs

    def builtin_for_runtime(runtime: str) -> tuple[str, str]:
        if runtime == "python3.11":
            return "handler", COMPLEX_PYTHON_HANDLER_CODE
        if runtime == "nodejs20":
            return "handler", COMPLEX_NODE_HANDLER_CODE
        if runtime == "go1.24":
            arch = docker_server_arch()
            return "handler", build_go_binary_b64(arch=arch, artifact_dir=args.artifact_dir, source=COMPLEX_GO_STDIN_HANDLER_SOURCE)
        if runtime == "wasm":
            return "handle", build_wasm_b64(artifact_dir=args.artifact_dir, source=COMPLEX_RUST_WASM_HANDLER_SOURCE)
        raise SystemExit(f"unsupported runtime for built-in deploy: {runtime!r}")

    if args.deploy:
        if is_mixed:
            for s in specs:
                runtime = s.runtime
                if runtime is None:
                    raise SystemExit(f"target {s.name!r} missing runtime; use --target {s.name}@python3.11:1")
                handler, code = builtin_for_runtime(runtime)
                ensure_function(
                    admin,
                    name=s.name,
                    runtime=runtime,
                    handler=handler,
                    code=code,
                    description=args.description,
                    memory_mb=args.memory_mb,
                    timeout_sec=args.timeout_sec,
                    env_vars={},
                    force_recreate=args.force_recreate,
                )
        else:
            runtime = args.runtime
            handler = args.handler
            if args.code_file:
                code = open(args.code_file, "r", encoding="utf-8").read()
            else:
                builtin_handler, code = builtin_for_runtime(runtime)
                if handler == "handler" and builtin_handler != "handler":
                    handler = builtin_handler
            ensure_function(
                admin,
                name=args.function,
                runtime=runtime,
                handler=handler,
                code=code,
                description=args.description,
                memory_mb=args.memory_mb,
                timeout_sec=args.timeout_sec,
                env_vars={},
                force_recreate=args.force_recreate,
            )

    if args.payload:
        payload = json.loads(args.payload)
        if not isinstance(payload, dict):
            raise SystemExit("--payload must be a JSON object")
    else:
        payload = {"n": args.n, "loops": args.loops, "payload_kb": args.payload_kb, "seed": args.seed}

    targets = make_targets(specs)

    target_meta: dict[str, dict[str, Any]] = {}
    for t in targets:
        fn = get_function(admin, t.name)
        if fn is None:
            target_meta[t.name] = {"runtime": t.runtime}
        else:
            target_meta[t.name] = {"runtime": fn.get("runtime") or t.runtime, "id": fn.get("id")}

    if args.warmup > 0:
        warm_payload = dict(payload)
        if not args.no_auto_fields:
            warm_payload["seq"] = -1
            warm_payload["seed"] = warm_payload.get("seed", 1) & 0xFFFFFFFF
        for t in targets:
            for _ in range(args.warmup):
                admin.request_json("POST", t.path, warm_payload)

    start_at = time.perf_counter() + 0.25
    max_requests: Optional[int]
    end_at: Optional[float]
    if args.duration and args.duration > 0:
        end_at = start_at + args.duration
        max_requests = None
    else:
        if args.requests <= 0:
            raise SystemExit("set --requests > 0 or set --duration > 0")
        max_requests = args.requests
        end_at = None

    qps_per_worker: Optional[float] = None
    if args.qps and args.qps > 0:
        qps_per_worker = args.qps / float(args.concurrency)

    next_request_id = [0]
    next_lock = threading.Lock()

    workers: list[threading.Thread] = []
    results: list[WorkerResult] = []
    results_lock = threading.Lock()

    def _thread_main(worker_id: int, ramp_delay_s: float) -> None:
        # Add per-worker jitter to avoid lockstep behavior.
        rnd = random.Random(worker_id)
        jitter = rnd.uniform(0.0, 0.02)
        res = run_worker(
            worker_id=worker_id,
            base=base,
            targets=targets,
            payload=payload,
            auto_fields=not args.no_auto_fields,
            timeout=args.timeout,
            insecure_tls=args.insecure_tls,
            start_at=start_at,
            ramp_delay_s=ramp_delay_s + jitter,
            end_at=end_at,
            next_request_id=next_request_id,
            next_lock=next_lock,
            max_requests=max_requests,
            qps_per_worker=qps_per_worker,
            ok_min=args.ok_min,
            ok_max=args.ok_max,
            max_error_samples=args.max_error_samples,
        )
        with results_lock:
            results.append(res)

    for i in range(args.concurrency):
        delay = 0.0
        if args.ramp_up and args.ramp_up > 0:
            delay = (args.ramp_up * i) / float(max(1, args.concurrency-1))
        t = threading.Thread(target=_thread_main, args=(i, delay), daemon=True)
        workers.append(t)

    wall_start = start_at
    for t in workers:
        t.start()
    for t in workers:
        t.join()
    wall_end = time.perf_counter()

    admin.close()

    total_reqs = sum(r.requests for r in results)
    total_ok = sum(r.ok for r in results)
    total_cold = sum(r.cold_starts for r in results)
    status_counts: Counter[int] = Counter()
    latencies_ms: list[float] = []
    error_samples: list[str] = []
    by_target: dict[str, WorkerResult] = {}
    for r in results:
        status_counts.update(r.statuses)
        latencies_ms.extend(r.latencies_ms)
        for name, tr in r.by_target.items():
            agg = by_target.get(name)
            if agg is None:
                agg = WorkerResult(
                    requests=0,
                    ok=0,
                    statuses=Counter(),
                    cold_starts=0,
                    latencies_ms=[],
                    error_samples=[],
                    by_target={},
                )
                by_target[name] = agg
            agg.requests += tr.requests
            agg.ok += tr.ok
            agg.cold_starts += tr.cold_starts
            agg.statuses.update(tr.statuses)
            agg.latencies_ms.extend(tr.latencies_ms)
        for e in r.error_samples:
            if len(error_samples) < args.max_error_samples:
                error_samples.append(e)

    latencies_ms.sort()
    for tr in by_target.values():
        tr.latencies_ms.sort()
    wall_s = max(1e-9, wall_end - wall_start)
    rps = total_reqs / wall_s

    report = {
        "mode": "mixed" if is_mixed else "single",
        "base_url": args.base_url,
        "function": None if is_mixed else args.function,
        "concurrency": args.concurrency,
        "requests": total_reqs,
        "ok": total_ok,
        "errors": total_reqs - total_ok,
        "cold_starts": total_cold,
        "wall_time_s": wall_s,
        "throughput_rps": rps,
        "statuses": {str(k): v for k, v in sorted(status_counts.items(), key=lambda kv: kv[0])},
        "latency_ms": {
            "min": latencies_ms[0] if latencies_ms else None,
            "p50": _pct(latencies_ms, 50),
            "p90": _pct(latencies_ms, 90),
            "p95": _pct(latencies_ms, 95),
            "p99": _pct(latencies_ms, 99),
            "max": latencies_ms[-1] if latencies_ms else None,
        },
        "targets": {
            name: {
                "runtime": (target_meta.get(name) or {}).get("runtime"),
                "weight": next((t.weight for t in targets if t.name == name), 1),
                "requests": tr.requests,
                "ok": tr.ok,
                "errors": tr.requests - tr.ok,
                "cold_starts": tr.cold_starts,
                "throughput_rps": tr.requests / wall_s,
                "statuses": {str(k): v for k, v in sorted(tr.statuses.items(), key=lambda kv: kv[0])},
                "latency_ms": {
                    "min": tr.latencies_ms[0] if tr.latencies_ms else None,
                    "p50": _pct(tr.latencies_ms, 50),
                    "p90": _pct(tr.latencies_ms, 90),
                    "p95": _pct(tr.latencies_ms, 95),
                    "p99": _pct(tr.latencies_ms, 99),
                    "max": tr.latencies_ms[-1] if tr.latencies_ms else None,
                },
            }
            for name, tr in by_target.items()
        },
        "error_samples": error_samples,
    }

    if is_mixed:
        print(
            f"base={args.base_url} targets={len(targets)} conc={args.concurrency} "
            f"reqs={total_reqs} ok={total_ok} err={total_reqs - total_ok} "
            f"cold={total_cold} wall={wall_s:.3f}s rps={rps:.1f}"
        )
    else:
        print(
            f"base={args.base_url} fn={args.function} conc={args.concurrency} "
            f"reqs={total_reqs} ok={total_ok} err={total_reqs - total_ok} "
            f"cold={total_cold} wall={wall_s:.3f}s rps={rps:.1f}"
        )
    if latencies_ms:
        print(
            "latency_ms:"
            f" min={report['latency_ms']['min']:.2f}"
            f" p50={report['latency_ms']['p50']:.2f}"
            f" p90={report['latency_ms']['p90']:.2f}"
            f" p95={report['latency_ms']['p95']:.2f}"
            f" p99={report['latency_ms']['p99']:.2f}"
            f" max={report['latency_ms']['max']:.2f}"
        )
    if is_mixed and by_target:
        print("targets:")
        for t in sorted(targets, key=lambda x: x.name):
            tr = by_target.get(t.name)
            if tr is None:
                continue
            rt = (target_meta.get(t.name) or {}).get("runtime")
            p50 = _pct(tr.latencies_ms, 50)
            cold = tr.cold_starts
            p50_s = f"{p50:.2f}ms" if p50 is not None else "n/a"
            print(
                f"  - {t.name} runtime={rt} weight={t.weight} reqs={tr.requests} "
                f"ok={tr.ok} err={tr.requests - tr.ok} cold={cold} p50={p50_s}"
            )
    print("statuses:", " ".join(f"{k}={v}" for k, v in sorted(report["statuses"].items(), key=lambda kv: int(kv[0]))))
    if error_samples:
        print("errors (sample):")
        for e in error_samples:
            print("  -", e)

    if args.json_out:
        os.makedirs(os.path.dirname(args.json_out) or ".", exist_ok=True)
        with open(args.json_out, "w", encoding="utf-8") as f:
            json.dump(report, f, ensure_ascii=False, indent=2)
        print("wrote:", args.json_out)

    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        print("interrupted", file=sys.stderr)
        raise SystemExit(130)
