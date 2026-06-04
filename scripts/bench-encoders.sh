#!/usr/bin/env bash
#
# bench-encoders.sh - compare go-flac against libFLAC, SoX, and ffmpeg on the
# same input: encode at compression level 5, single-threaded, measuring wall
# time, CPU seconds, and peak RSS (GNU time), plus the resulting compression
# ratio. This is the re-runnable basis for the v0.1.0 performance baseline.
#
# Usage:
#   scripts/bench-encoders.sh [input.wav]
#
# With no argument it generates a deterministic 30-minute mono 48 kHz/16-bit
# tone+noise mix (ffmpeg, fixed seed) so the run is reproducible across machines.
#
# Requires: go and GNU time. flac, sox, and ffmpeg are each optional and skipped
# with a note if absent (ffmpeg is also used to synthesize the default input).
#
# This script only ever reads its input and writes outputs into a private temp
# directory; it never deletes or modifies the input file.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

have() { command -v "$1" >/dev/null 2>&1; }

gnu_time=""
for t in /usr/bin/time time; do
  if "$t" -f '%e' true >/dev/null 2>&1; then gnu_time="$t"; break; fi
done
[ -n "$gnu_time" ] || { echo "GNU time not found (install the 'time' package)"; exit 1; }
have go || { echo "go not found"; exit 1; }

input="${1:-}"
if [ -z "$input" ]; then
  have ffmpeg || { echo "ffmpeg needed to generate the default input"; exit 1; }
  input="$work/bench_input.wav"
  echo "generating deterministic 30-min mono 48 kHz/16-bit input..."
  ffmpeg -hide_banner -loglevel error -y \
    -f lavfi -i "sine=frequency=440:sample_rate=48000:duration=1800" \
    -f lavfi -i "anoisesrc=sample_rate=48000:amplitude=0.25:duration=1800:seed=42" \
    -filter_complex "amix=inputs=2" -ac 1 -ar 48000 -c:a pcm_s16le "$input"
fi
[ -f "$input" ] || { echo "input not found: $input"; exit 1; }
insz=$(stat -c%s "$input")
echo "input: $input ($insz bytes)"

echo "building wav2flac..."
go build -o "$work/wav2flac" "$repo_root/cmd/wav2flac"

cat "$input" >/dev/null # warm the page cache

# bench NAME OUTFILE -- CMD...   (the output path is explicit; the input is read-only)
bench() {
  local name="$1" out="$2"; shift 2
  [ "$1" = "--" ] && shift
  if "$gnu_time" -f '%e %U %S %P %M' -o "$work/tm" "$@" >"$work/log" 2>&1; then
    read -r e u s p m <"$work/tm"
    local osz ratio cpu thru
    osz=$(stat -c%s "$out")
    ratio=$(awk "BEGIN{printf \"%.4f\", $osz/$insz}")
    cpu=$(awk "BEGIN{printf \"%.2f\", $u+$s}")
    thru=$(awk "BEGIN{e=$e; if (e>0) printf \"%.1f\", ($insz/1048576)/e; else printf \"n/a\"}")
    printf '%-9s wall=%ss cpu=%ss %%cpu=%s rss=%sKB thru=%s MB/s ratio=%s\n' \
      "$name" "$e" "$cpu" "$p" "$m" "$thru" "$ratio"
  else
    printf '%-9s FAILED (see output below)\n' "$name"; cat "$work/log"
  fi
}

echo "=== encode, level 5, single-threaded ==="
bench go-flac "$work/o_go.flac"  -- "$work/wav2flac" -level 5 "$input" "$work/o_go.flac"
if have flac; then
  bench libFLAC "$work/o_flac.flac" -- flac -5 -f -s -o "$work/o_flac.flac" "$input"
else echo "libFLAC: skipped (flac not installed)"; fi
if have sox; then
  bench sox "$work/o_sox.flac" -- sox "$input" -C 5 "$work/o_sox.flac"
else echo "sox: skipped (not installed)"; fi
if have ffmpeg; then
  bench ffmpeg "$work/o_ff.flac" -- ffmpeg -hide_banner -loglevel error -y -threads 1 \
    -i "$input" -c:a flac -compression_level 5 "$work/o_ff.flac"
else echo "ffmpeg: skipped (not installed)"; fi
