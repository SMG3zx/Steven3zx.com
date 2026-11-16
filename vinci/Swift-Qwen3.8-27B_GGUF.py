#!/usr/bin/env python3
"""
tune_swift.py - find the fastest llama-server settings for a GGUF model that
still leave headroom on VRAM and RAM.

What it does
  1. Uses a local copy of each requested quant if one is already in --models-dir
     (no download). Otherwise downloads it from Hugging Face.
  2. Searches the knobs below. For every candidate it finds the highest number of
     GPU layers (-ngl) that keeps BOTH
         GPU memory used  <= (1 - headroom) * total VRAM
         system RAM used  <= (1 - headroom) * total RAM
     Memory is sampled system-wide every 0.2 s while the model loads and while it
     generates, and the server is killed the instant a limit is crossed.
  3. Benchmarks the winning ngl of each candidate (prompt + generation tokens/sec).
  4. Prints tables, saves results.json and a run.log, and writes a launch script.

Knobs (each takes a list; the first value is the starting point)
  --kv        KV cache type, "K" or "K:V" (e.g. q8_0  q4_0  q8_0:q4_0)
  --ubatch    physical batch size (-ub)
  --threads   CPU threads (-t); default: physical cores, cores-1, cores-2
  --batch     logical batch size (-b)
  --fa        flash attention: on off auto
  --load-mode weight loading: none (no mmap) or mmap  (-lm; older builds: --no-mmap)
  --extra     raw extra llama-server args, one candidate per string
              e.g. --extra "" "--spec-type ngram-simple" "--override-tensor ffn_.*=CPU"
  --ctx       context sizes
  --quants    quants to tune

Search modes
  --search coord  (default) start from the first value of every knob, then try each
                  other value of one knob at a time and keep the faster one.
  --search grid   try every combination (can take many hours).

Requirements
  pip install huggingface_hub psutil gguf
  llama.cpp's llama-server (CUDA build) on PATH, or pass --llama-server PATH
  nvidia-smi on PATH (comes with the NVIDIA driver)

Examples
  python tune_swift.py
  python tune_swift.py --quants IQ3_XXS --ctx 32768 --ubatch 256 512 --threads 6 4
  python tune_swift.py --search grid --kv q8_0 q4_0 --fa on off -v
"""
import argparse
import itertools
import json
import re
import shlex
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from dataclasses import asdict, dataclass, replace
from pathlib import Path

try:
    import psutil
except ImportError:
    sys.exit("Missing dependency. Run: pip install huggingface_hub psutil gguf")

DEFAULT_REPO = "ukisai/Swift-Qwen3.8-27B-GGUF"
OOM_PATTERNS = re.compile(
    r"out of memory|failed to allocate|cudamalloc failed|unable to allocate|"
    r"cuda error|ggml_backend_cuda_buffer_type_alloc_buffer|failed to load model",
    re.I,
)
# llama-server log lines worth showing live without -v
LLAMA_KEEP = re.compile(
    r"offload|buffer size|KV |compute buffer|n_ctx|n_batch|n_ubatch|error|warn|"
    r"listening|CUDA|device|model loaded|tokens per second|eval time|failed",
    re.I,
)
KNOB_ORDER = ["kv", "ub", "threads", "batch", "fa", "lm", "extra"]
START = time.time()
RUN_LOG = None


# --------------------------------------------------------------------------- #
# Logging (every line is printed live and appended to run.log)
# --------------------------------------------------------------------------- #
def log(msg=""):
    line = f"[{time.strftime('%H:%M:%S')} +{time.time() - START:6.0f}s] {msg}" if msg else ""
    if sys.stdout.isatty():
        sys.stdout.write("\r\x1b[2K")  # clear any in-place status line first
    print(line, flush=True)
    if RUN_LOG is not None:
        with open(RUN_LOG, "a") as f:
            f.write(line + "\n")


def status(text):
    """In-place status line on a terminal; a normal log line otherwise."""
    if sys.stdout.isatty():
        sys.stdout.write("\r\x1b[2K      " + text)
        sys.stdout.flush()
    else:
        log("      " + text)


# --------------------------------------------------------------------------- #
# Data
# --------------------------------------------------------------------------- #
@dataclass(frozen=True)
class Knobs:
    kv: str = "q8_0"       # "K" or "K:V"
    ub: int = 512
    threads: int = 6
    batch: int = 2048
    fa: str = "on"
    lm: str = "none"
    extra: str = ""

    def kv_pair(self):
        k, _, v = self.kv.partition(":")
        return k, (v or k)

    def short(self):
        extra = self.extra.strip() or "-"
        return (f"kv={self.kv} ub={self.ub} t={self.threads} b={self.batch} "
                f"fa={self.fa} lm={self.lm} extra={extra}")


@dataclass
class Trial:
    ok: bool
    reason: str = ""            # "", "vram", "ram", "error"
    ngl: int = 0
    vram_peak_mib: int = 0
    ram_peak_gib: float = 0.0
    pp_tps: float = 0.0
    tg_tps: float = 0.0
    prompt_tokens: int = 0
    tail: str = ""


@dataclass
class ConfigResult:
    quant: str
    ctx: int
    kv: str
    ub: int
    threads: int
    batch: int
    fa: str
    lm: str
    extra: str
    ngl: int
    vram_peak_mib: int
    vram_pct: float
    ram_peak_gib: float
    ram_pct: float
    pp_tps: float
    tg_tps: float
    loads: int                  # model loads spent on this candidate

    def knobs(self):
        return Knobs(self.kv, self.ub, self.threads, self.batch, self.fa, self.lm, self.extra)


# --------------------------------------------------------------------------- #
# System probes
# --------------------------------------------------------------------------- #
def gpu_mem(idx):
    """Return (used_MiB, total_MiB) for GPU idx via nvidia-smi."""
    out = subprocess.run(
        ["nvidia-smi", f"--id={idx}", "--query-gpu=memory.used,memory.total",
         "--format=csv,noheader,nounits"],
        capture_output=True, text=True, timeout=15,
    )
    if out.returncode != 0:
        raise RuntimeError(f"nvidia-smi failed: {out.stderr.strip()}")
    used, total = (int(x.strip()) for x in out.stdout.strip().splitlines()[0].split(","))
    return used, total


def ram_used_bytes():
    vm = psutil.virtual_memory()
    return vm.total - vm.available


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def model_layers(path):
    """Read block_count from the GGUF header; fall back to 64."""
    try:
        from gguf import GGUFReader
        reader = GGUFReader(str(path))
        for name, field in reader.fields.items():
            if name.endswith(".block_count"):
                return int(field.parts[field.data[0]][0])
    except Exception as e:  # noqa: BLE001
        log(f"  (could not read layer count from GGUF: {e}; assuming 64)")
    return 64


# --------------------------------------------------------------------------- #
# Model files: use local copies when they exist, download only when missing
# --------------------------------------------------------------------------- #
def quant_pattern(quant):
    return re.compile(rf"(?<![A-Za-z0-9]){re.escape(quant)}(?![A-Za-z0-9])", re.I)


def find_local(dest, quant):
    """Local GGUF files for quant (all shards, sorted), or [] if absent or incomplete."""
    pat = quant_pattern(quant)
    files = sorted(p for p in Path(dest).rglob("*.gguf")
                   if pat.search(p.name) and "mmproj" not in p.name.lower())
    if not files:
        return []
    m = re.search(r"-(\d+)-of-(\d+)", files[0].name)
    if m and len(files) < int(m.group(2)):
        log(f"  local {quant} has {len(files)} of {m.group(2)} shards; will download the rest")
        return []
    return files


def get_quant(repo, quant, dest, redownload):
    if not redownload:
        local = find_local(dest, quant)
        if local:
            size = sum(p.stat().st_size for p in local) / 2**30
            log(f"  already have {local[0]} ({len(local)} file(s), {size:.1f} GiB) - skipping download")
            return local[0]
    from huggingface_hub import hf_hub_download, list_repo_files

    log(f"  listing {repo} on Hugging Face ...")
    files = [f for f in list_repo_files(repo)
             if f.lower().endswith(".gguf") and "mmproj" not in f.lower()]
    matches = sorted(f for f in files if quant_pattern(quant).search(Path(f).name))
    if not matches:
        raise SystemExit(f"No GGUF matching '{quant}' in {repo}. Available:\n  " + "\n  ".join(files))
    paths = []
    for f in matches:  # more than one file means a sharded model; get every shard
        log(f"  downloading {f} into {dest} ...")
        paths.append(hf_hub_download(repo, f, local_dir=dest))
    log(f"  downloaded {len(paths)} file(s)")
    return Path(paths[0])  # llama.cpp loads the remaining shards from the first


# --------------------------------------------------------------------------- #
# Server runner
# --------------------------------------------------------------------------- #
class Tuner:
    def __init__(self, args):
        self.a = args
        self.llama = args.llama_server
        self.vram_total = gpu_mem(args.gpu)[1]
        self.ram_total = psutil.virtual_memory().total
        self.vram_limit = int(self.vram_total * (1 - args.headroom))
        self.ram_limit = int(self.ram_total * (1 - args.headroom))
        self.log_dir = Path(tempfile.mkdtemp(prefix="tune_swift_logs_"))
        self.trials_run = 0
        self.baseline_vram = 0
        self.results = []
        self._live = (0, 0)

        text = self._help_text()
        self.new_load_mode = "--load-mode" in text
        if not self.new_load_mode and "--no-mmap" not in text:
            sys.exit("llama-server supports neither --no-mmap nor --load-mode; "
                     "cannot control mmap. Update llama.cpp.")
        self.new_fa = "[on|off|auto]" in text

        log(f"VRAM: {self.vram_total} MiB total, hard limit {self.vram_limit} MiB "
            f"({100 * (1 - args.headroom):.0f}%)")
        log(f"RAM : {self.ram_total / 2**30:.1f} GiB total, hard limit "
            f"{self.ram_limit / 2**30:.1f} GiB ({100 * (1 - args.headroom):.0f}%)")
        log(f"llama-server: {self.llama}  (load flag: "
            f"{'-lm' if self.new_load_mode else '--no-mmap'}, fa: "
            f"{'[on|off|auto]' if self.new_fa else 'boolean'})")
        log(f"per-trial logs: {self.log_dir}")

    def _help_text(self):
        r = subprocess.run([self.llama, "--help"], capture_output=True, text=True)
        return (r.stdout or "") + (r.stderr or "")

    def knob_flags(self, k):
        kk, vv = k.kv_pair()
        f = ["-ctk", kk, "-ctv", vv, "-t", str(k.threads), "-b", str(k.batch),
             "-ub", str(k.ub), "-np", "1"]
        if self.new_load_mode:
            f += ["-lm", k.lm]
        elif k.lm == "none":
            f += ["--no-mmap"]
        if self.new_fa:
            f += ["-fa", k.fa]
        elif k.fa == "on":
            f += ["-fa"]
        if k.extra.strip():
            f += shlex.split(k.extra)
        return f

    def build_cmd(self, model, ngl, ctx, knobs, port=None):
        cmd = [self.llama, "-m", str(model), "-ngl", str(ngl), "-c", str(ctx)]
        cmd += self.knob_flags(knobs)
        if port is not None:
            cmd += ["--host", "127.0.0.1", "--port", str(port)]
        return cmd

    # -- one load (+ optional benchmark) at a fixed config ------------------- #
    def trial(self, model, ngl, ctx, knobs, bench=False):
        self.trials_run += 1
        n = self.trials_run
        port = free_port()
        cmd = self.build_cmd(model, ngl, ctx, knobs, port)
        log_path = self.log_dir / f"trial{n:04d}_ngl{ngl}_ctx{ctx}.log"
        kind = "bench" if bench else "probe"
        log(f"  [trial {n}] {kind} ngl={ngl} ctx={ctx} {knobs.short()}")
        if self.a.verbose:
            log("    $ " + shlex.join(cmd))

        t0 = time.time()
        peak_v, peak_r = 0, 0
        proc = None
        offset = 0
        res = Trial(ok=False, ngl=ngl)

        def sample():
            nonlocal peak_v, peak_r
            v = gpu_mem(self.a.gpu)[0]
            r = ram_used_bytes()
            peak_v, peak_r = max(peak_v, v), max(peak_r, r)
            self._live = (v, r)
            if v > self.vram_limit:
                return "vram"
            if r > self.ram_limit:
                return "ram"
            return ""

        def pump():
            """Show new llama-server log lines as they are written."""
            nonlocal offset
            try:
                with open(log_path, "rb") as f:
                    f.seek(offset)
                    chunk = f.read()
            except FileNotFoundError:
                return
            offset += len(chunk)
            for line in chunk.decode(errors="ignore").splitlines():
                line = line.strip()
                if line and (self.a.verbose or LLAMA_KEEP.search(line)):
                    log("      | " + line[:200])

        def live(label):
            v, r = self._live
            return (f"{label} {time.time() - t0:5.1f}s  VRAM {v:>5}/{self.vram_limit} MiB  "
                    f"RAM {r / 2**30:5.1f}/{self.ram_limit / 2**30:.1f} GiB")

        def finish(reason):
            res.reason = reason
            log(f"      stop: {reason or 'ok'} after {time.time() - t0:.1f}s, "
                f"peak VRAM {peak_v} MiB, peak RAM {peak_r / 2**30:.1f} GiB")
            res.vram_peak_mib, res.ram_peak_gib = peak_v, peak_r / 2**30
            return res

        try:
            with open(log_path, "wb") as lf:
                proc = subprocess.Popen(cmd, stdout=lf, stderr=subprocess.STDOUT)
            deadline = time.time() + self.a.load_timeout
            last_tick = 0.0
            while True:
                bad = sample()
                pump()
                if bad:
                    log(f"      !! {bad} limit crossed while loading")
                    return finish(bad)
                if proc.poll() is not None:
                    pump()
                    tail = log_path.read_text(errors="ignore")[-3000:]
                    res.tail = tail
                    reason = "vram" if OOM_PATTERNS.search(tail) else "error"
                    log(f"      !! llama-server exited with code {proc.returncode} -> {reason}")
                    return finish(reason)
                if time.time() > deadline:
                    res.tail = log_path.read_text(errors="ignore")[-3000:]
                    log(f"      !! load timed out after {self.a.load_timeout}s")
                    return finish("error")
                try:
                    with urllib.request.urlopen(f"http://127.0.0.1:{port}/health", timeout=2) as h:
                        if h.status == 200:
                            break
                except (urllib.error.URLError, ConnectionError, socket.timeout, OSError):
                    pass
                if time.time() - last_tick >= self.a.status_interval:
                    status(live("loading"))
                    last_tick = time.time()
                time.sleep(0.2)

            log(f"      model loaded in {time.time() - t0:.1f}s; peak VRAM {peak_v} MiB "
                f"({100 * peak_v / self.vram_total:.1f}%), peak RAM {peak_r / 2**30:.1f} GiB")
            pump()

            if bench:
                requests = ([("warm", 256, 16)] +
                            [("bench", min(2048, ctx - 256), self.a.n_predict)] * self.a.repeats)
            else:
                requests = [("fit", min(1024, ctx - 256), 16)]
            pp, tg, ptoks = [], [], 0
            with ThreadPoolExecutor(max_workers=1) as ex:
                for i, (kind_r, prompt_tokens, n_predict) in enumerate(requests, 1):
                    log(f"      request {i}/{len(requests)} [{kind_r}]: "
                        f"~{prompt_tokens} prompt tokens, {n_predict} generated")
                    rt = time.time()
                    fut = ex.submit(self._completion, port, prompt_tokens, n_predict)
                    last_tick = 0.0
                    while not fut.done():
                        bad = sample()
                        pump()
                        if bad:
                            log(f"      !! {bad} limit crossed while generating")
                            return finish(bad)
                        if proc.poll() is not None:
                            log("      !! llama-server died while generating")
                            return finish("vram")
                        if time.time() - last_tick >= self.a.status_interval:
                            status(live("generating"))
                            last_tick = time.time()
                        time.sleep(0.2)
                    out = fut.result()
                    pump()
                    log(f"      done [{kind_r}] in {time.time() - rt:.1f}s: prompt "
                        f"{out['prompt_tokens']} tok @ {out['pp']:.1f} tok/s, generated "
                        f"{out['n_gen']} tok @ {out['tg']:.2f} tok/s")
                    if kind_r == "bench":
                        pp.append(out["pp"])
                        tg.append(out["tg"])
                        ptoks = out["prompt_tokens"]
            res.ok = True
            if pp:
                res.pp_tps = sum(pp) / len(pp)
                res.tg_tps = sum(tg) / len(tg)
                res.prompt_tokens = ptoks
            return finish("")
        except Exception as e:  # noqa: BLE001
            res.tail = str(e)
            log(f"      !! exception: {e}")
            return finish("error")
        finally:
            if proc is not None:
                self._kill(proc)

    def _completion(self, port, prompt_tokens, n_predict):
        words = max(8, int(prompt_tokens / 1.3))
        prompt = ("The quick brown fox jumps over the lazy dog. " * (words // 9 + 1))
        prompt = " ".join(prompt.split()[:words])
        body = json.dumps({
            "prompt": prompt, "n_predict": n_predict, "temperature": 0,
            "cache_prompt": False, "stream": False, "ignore_eos": True,
        }).encode()
        req = urllib.request.Request(f"http://127.0.0.1:{port}/completion", data=body,
                                     headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=600) as r:
            data = json.loads(r.read())
        t = data.get("timings", {})
        return {"pp": float(t.get("prompt_per_second", 0)),
                "tg": float(t.get("predicted_per_second", 0)),
                "prompt_tokens": int(t.get("prompt_n", data.get("tokens_evaluated", 0))),
                "n_gen": int(t.get("predicted_n", n_predict))}

    def _kill(self, proc):
        try:
            for c in psutil.Process(proc.pid).children(recursive=True):
                c.kill()
        except psutil.Error:
            pass
        try:
            proc.kill()
            proc.wait(timeout=30)
        except Exception:  # noqa: BLE001
            pass
        # let the driver release VRAM before the next trial
        for _ in range(60):
            used = gpu_mem(self.a.gpu)[0]
            if used <= self.baseline_vram + 400:
                break
            if self.a.verbose:
                log(f"      waiting for VRAM to free ({used} MiB in use)")
            time.sleep(0.25)

    # -- search over ngl ----------------------------------------------------- #
    def find_max_ngl(self, model, n_layers, ctx, knobs, hint):
        """Highest ngl that passes both limits. Gallops out from the hint, then bisects.

        Passing is a window: too few GPU layers can break the RAM limit, too many can
        break the VRAM limit. Returns None if nothing passes or a non-memory error occurs.
        """
        lo, hi = 0, n_layers
        hi_known = False          # a VRAM failure has bounded the search from above
        best = None
        n = min(max(hint, 0), n_layers)
        step = 1
        while lo <= hi:
            t = self.trial(model, n, ctx, knobs)
            if t.ok:
                best = n if best is None else max(best, n)
                lo = n + 1
            elif t.reason == "ram":
                lo = n + 1
            elif t.reason == "vram":
                hi, hi_known = n - 1, True
            else:
                log(f"    ngl={n} failed for a non-memory reason; abandoning this candidate")
                log("    last llama-server output:\n" + (t.tail or "(none)")[-1200:])
                return None
            if lo > hi:
                break
            if t.ok or t.reason == "ram":
                if not hi_known:                      # gallop up
                    n = min(n + step, hi)
                    step *= 2
                    continue
            elif best is None:                        # nothing passes yet: gallop down
                n = max(n - step, lo)
                step *= 2
                continue
            n = (lo + hi) // 2                        # bisect the bracket
        return best

    def evaluate(self, quant, model, n_layers, ctx, knobs, hint):
        """Find the best ngl for one candidate, benchmark it, and return a ConfigResult."""
        before = self.trials_run
        log(f"\n  >> candidate: {quant} ctx={ctx} {knobs.short()}")
        hint = hint if hint is not None else (n_layers * 3) // 4
        best = self.find_max_ngl(model, n_layers, ctx, knobs, hint)
        if best is None:
            log("     no ngl fits this candidate; skipped")
            return None
        log(f"     best fitting ngl={best}; running benchmark")
        t = None
        while best >= 0:
            t = self.trial(model, best, ctx, knobs, bench=True)
            if t.ok:
                break
            log(f"     bench at ngl={best} failed ({t.reason}); trying ngl={best - 1}")
            best -= 1
        if best < 0 or t is None or not t.ok:
            log("     benchmark failed at every ngl; skipped")
            return None
        r = ConfigResult(
            quant, ctx, knobs.kv, knobs.ub, knobs.threads, knobs.batch, knobs.fa, knobs.lm,
            knobs.extra, best, t.vram_peak_mib,
            round(100 * t.vram_peak_mib * 2**20 / (self.vram_total * 2**20), 1),
            round(t.ram_peak_gib, 2),
            round(100 * t.ram_peak_gib * 2**30 / self.ram_total, 1),
            round(t.pp_tps, 1), round(t.tg_tps, 2), self.trials_run - before)
        self.results.append(r)
        log(f"     => ngl={best}  gen {r.tg_tps} tok/s  prompt {r.pp_tps} tok/s  "
            f"VRAM {r.vram_pct}%  RAM {r.ram_pct}%  ({r.loads} loads)")
        return r


# --------------------------------------------------------------------------- #
# Launch script
# --------------------------------------------------------------------------- #
def write_launch_scripts(tuner, model, best, out_dir):
    cmd = tuner.build_cmd(model, best.ngl, best.ctx, best.knobs(), port=8080)
    cmd += ["--jinja", "--temp", "1.0", "--top-p", "0.95", "--top-k", "20",
            "--min-p", "0", "--presence-penalty", "0", "--repeat-penalty", "1.0"]
    quoted = shlex.join(cmd)
    sh = out_dir / "run_best.sh"
    sh.write_text("#!/bin/sh\n" + quoted + "\n")
    bat = out_dir / "run_best.bat"
    bat.write_text("@echo off\r\n" + quoted + "\r\n")
    try:
        sh.chmod(0o755)
    except OSError:
        pass
    return quoted


# --------------------------------------------------------------------------- #
# Main
# --------------------------------------------------------------------------- #
def dedupe(seq):
    seen, out = set(), []
    for x in seq:
        if x not in seen:
            seen.add(x)
            out.append(x)
    return out


def main():
    global RUN_LOG
    cores = psutil.cpu_count(logical=False) or 4
    default_threads = dedupe(t for t in (cores, cores - 1, cores - 2) if t >= 1)

    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--repo", default=DEFAULT_REPO)
    ap.add_argument("--quants", nargs="+", default=["IQ4_XS", "IQ3_XXS"])
    ap.add_argument("--ctx", nargs="+", type=int, default=[16384, 32768])
    ap.add_argument("--min-ctx", type=int, default=16384, help="winner must have at least this context")
    ap.add_argument("--kv", nargs="+", default=["q8_0", "q4_0"],
                    help='KV cache types: "K" or "K:V" (e.g. q8_0 q8_0:q4_0)')
    ap.add_argument("--ubatch", nargs="+", type=int, default=[512, 256, 1024],
                    help="physical batch sizes (-ub)")
    ap.add_argument("--threads", nargs="+", type=int, default=default_threads,
                    help="CPU threads (-t)")
    ap.add_argument("--batch", nargs="+", type=int, default=[2048, 512],
                    help="logical batch sizes (-b)")
    ap.add_argument("--fa", nargs="+", default=["on", "off"], choices=["on", "off", "auto"],
                    help="flash attention settings")
    ap.add_argument("--load-mode", nargs="+", default=["none", "mmap"], choices=["none", "mmap"],
                    help="none = no mmap (weights read into RAM); mmap = memory-mapped")
    ap.add_argument("--extra", nargs="+", default=["", "--spec-type ngram-simple"],
                    help='extra llama-server args per candidate, e.g. "" "--spec-type ngram-simple"')
    ap.add_argument("--search", choices=["coord", "grid"], default="coord",
                    help="coord: one knob at a time; grid: every combination")
    ap.add_argument("--headroom", type=float, default=0.05, help="fraction of VRAM and RAM left free")
    ap.add_argument("--llama-server", default=shutil.which("llama-server"))
    ap.add_argument("--models-dir", default="models")
    ap.add_argument("--out-dir", default="tune_results")
    ap.add_argument("--gpu", type=int, default=0)
    ap.add_argument("--n-predict", type=int, default=128)
    ap.add_argument("--repeats", type=int, default=2)
    ap.add_argument("--load-timeout", type=int, default=300)
    ap.add_argument("--status-interval", type=float, default=2.0,
                    help="seconds between live status lines while loading/generating")
    ap.add_argument("-v", "--verbose", action="store_true",
                    help="also show every llama-server log line and the full command of each trial")
    ap.add_argument("--redownload", action="store_true", help="ignore local copies and download again")
    args = ap.parse_args()

    if not args.llama_server:
        sys.exit("llama-server not found. Download a CUDA build from "
                 "https://github.com/ggml-org/llama.cpp/releases and put it on PATH, "
                 "or pass --llama-server PATH")
    if not shutil.which("nvidia-smi"):
        sys.exit("nvidia-smi not found. Install the NVIDIA driver.")
    devices = subprocess.run([args.llama_server, "--list-devices"], capture_output=True, text=True)
    listing = (devices.stdout or "") + (devices.stderr or "")
    if "CUDA" not in listing:
        sys.exit("llama-server has no CUDA device, so it would run on the CPU only.\n"
                 "Build llama.cpp with CUDA (cmake -B build -DGGML_CUDA=ON) and pass "
                 "--llama-server PATH to build/bin/llama-server.\n"
                 f"--list-devices said:\n{listing.strip()}")
    if not 0.01 <= args.headroom <= 0.5:
        sys.exit("--headroom should be between 0.01 and 0.5")

    out_dir = Path(args.out_dir)
    out_dir.mkdir(parents=True, exist_ok=True)
    RUN_LOG = out_dir / "run.log"
    RUN_LOG.write_text("")

    tuner = Tuner(args)
    tuner.baseline_vram = gpu_mem(args.gpu)[0]
    if tuner.baseline_vram > tuner.vram_limit or ram_used_bytes() > tuner.ram_limit:
        sys.exit("Your machine is already above the headroom limit. Close other apps and retry.")

    values = {
        "kv": dedupe(args.kv), "ub": dedupe(args.ubatch), "threads": dedupe(args.threads),
        "batch": dedupe(args.batch), "fa": dedupe(args.fa), "lm": dedupe(args.load_mode),
        "extra": dedupe(args.extra),
    }
    per_group = (len(list(itertools.product(*[values[k] for k in KNOB_ORDER])))
                 if args.search == "grid"
                 else 1 + sum(len(v) - 1 for v in values.values()))
    log("PLAN")
    log(f"  quants: {args.quants}   ctx: {args.ctx}   search: {args.search}")
    for k in KNOB_ORDER:
        shown = [v or "-" for v in values[k]]
        log(f"  {k:<8} {shown}")
    log(f"  up to {per_group} candidates per (quant, ctx), each with ~3-8 model loads")
    log(f"  min ctx for the winner: {args.min_ctx}; headroom {args.headroom:.0%}")

    model_paths = {}
    try:
        for quant in args.quants:
            log(f"\n=== {quant} ===")
            model = get_quant(args.repo, quant, args.models_dir, args.redownload)
            model_paths[quant] = model
            n_layers = model_layers(model)
            log(f"  {model.name}: {n_layers} layers")
            for ctx in args.ctx:
                log(f"\n--- {quant} ctx={ctx} ---")
                if args.search == "grid":
                    hint = None
                    for combo in itertools.product(*[values[k] for k in KNOB_ORDER]):
                        r = tuner.evaluate(quant, model, n_layers, ctx,
                                           Knobs(**dict(zip(KNOB_ORDER, combo))), hint)
                        if r:
                            hint = r.ngl
                    continue

                # coordinate search: start from the first value of each knob
                base = Knobs(**{k: values[k][0] for k in KNOB_ORDER})
                anchor_r = tuner.evaluate(quant, model, n_layers, ctx, base, None)
                if anchor_r is None:
                    # starting point does not fit: take the first single-knob change that does
                    for k in KNOB_ORDER:
                        for v in values[k][1:]:
                            anchor_r = tuner.evaluate(quant, model, n_layers, ctx,
                                                      replace(base, **{k: v}), None)
                            if anchor_r:
                                base = anchor_r.knobs()
                                break
                        if anchor_r:
                            break
                if anchor_r is None:
                    log(f"  nothing fits {quant} at ctx={ctx}; skipping")
                    continue
                anchor = anchor_r.knobs()
                log(f"\n  anchor for {quant} ctx={ctx}: {anchor.short()} -> {anchor_r.tg_tps} tok/s")
                for k in KNOB_ORDER:
                    for v in values[k]:
                        if v == getattr(anchor, k):
                            continue
                        r = tuner.evaluate(quant, model, n_layers, ctx,
                                           replace(anchor, **{k: v}), anchor_r.ngl)
                        if r and r.tg_tps > anchor_r.tg_tps:
                            log(f"  + {k}={v} beats the anchor ({r.tg_tps} > {anchor_r.tg_tps} tok/s); "
                                f"keeping it")
                            anchor, anchor_r = r.knobs(), r
                log(f"  coordinate result for {quant} ctx={ctx}: {anchor.short()} "
                    f"-> {anchor_r.tg_tps} tok/s at ngl={anchor_r.ngl}")
    except KeyboardInterrupt:
        log("\nInterrupted; reporting what finished so far.")

    results = tuner.results
    if not results:
        sys.exit("No configuration fit inside the headroom limits.")

    log("\n" + "=" * 118)
    log(f"{'quant':<9}{'ctx':>7} {'kv':<11}{'ub':>6}{'thr':>5}{'batch':>7}{'fa':>5}"
        f"{'lm':>6}  {'extra':<22}{'ngl':>4}{'gen t/s':>9}{'pp t/s':>9}{'VRAM%':>7}{'RAM%':>6}{'loads':>7}")
    for r in sorted(results, key=lambda x: -x.tg_tps):
        log(f"{r.quant:<9}{r.ctx:>7} {r.kv:<11}{r.ub:>6}{r.threads:>5}{r.batch:>7}{r.fa:>5}"
            f"{r.lm:>6}  {(r.extra.strip() or '-')[:22]:<22}{r.ngl:>4}{r.tg_tps:>9}{r.pp_tps:>9}"
            f"{r.vram_pct:>7.1f}{r.ram_pct:>6.1f}{r.loads:>7}")

    log("\nBest generation speed per knob value (within each quant and ctx):")
    for quant, ctx in sorted({(r.quant, r.ctx) for r in results}, key=lambda x: (x[0], x[1])):
        group = [r for r in results if r.quant == quant and r.ctx == ctx]
        log(f"  {quant} ctx={ctx}")
        for k in KNOB_ORDER:
            parts = []
            for v in values[k]:
                label = str(v).strip() or "-"
                c = [r for r in group if (str(getattr(r, k)).strip() or "-") == label]
                if c:
                    parts.append(f"{label}={max(x.tg_tps for x in c)}")
            if parts:
                log(f"    {k:<8} " + "  ".join(parts))

    eligible = [r for r in results if r.ctx >= args.min_ctx] or results
    best = max(eligible, key=lambda x: (x.tg_tps, x.ctx))
    cmd = write_launch_scripts(tuner, model_paths[best.quant], best, out_dir)
    (out_dir / "results.json").write_text(json.dumps([asdict(r) for r in results], indent=2))

    log(f"\nBEST: {best.quant}  ctx={best.ctx}  kv={best.kv}  ub={best.ub}  threads={best.threads}  "
        f"batch={best.batch}  fa={best.fa}  lm={best.lm}  extra={best.extra}  ngl={best.ngl}  "
        f"-> {best.tg_tps} tok/s generation")
    log(f"Launch script: {out_dir / 'run_best.bat'}  (or run_best.sh)")
    log(f"Command:\n  {cmd}")
    log(f"Raw results: {out_dir / 'results.json'}   Run log: {RUN_LOG}")


if __name__ == "__main__":
    main()
