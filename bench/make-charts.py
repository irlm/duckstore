#!/usr/bin/env python3
"""Draw the measured results as one self-contained HTML page.

    python3 bench/make-charts.py            # writes docs/results/charts.html

Reads what the benchmark and the load test already wrote in docs/results/ and draws SVG by
hand: no libraries, no network, so the page opens from a file:// URL and survives in git.

The charts follow the rules the rest of the project uses: one colour per engine everywhere,
a log scale whenever the values span more than two decades (they span five), the median of
the runs rather than the best one, and a table under every chart, because a chart that cannot
be read as numbers is decoration.
"""
from __future__ import annotations

import csv
import html
import math
import pathlib
import re
import statistics
from collections import defaultdict

ROOT = pathlib.Path(__file__).resolve().parent.parent
RESULTS = ROOT / "docs" / "results"
OUT = RESULTS / "charts.html"

# The engine colours of the app, validated for both modes (dataviz: light set / dark set).
ENGINES = ["duckdb", "mssql", "postgres", "pgduckdb"]
LABEL = {"duckdb": "DuckDB", "mssql": "SQL Server", "postgres": "Postgres", "pgduckdb": "pg_duckdb"}
COLOR = {"duckdb": "var(--orange)", "mssql": "var(--violet)", "postgres": "var(--blue)", "pgduckdb": "var(--aqua)"}

SCENARIOS = ["store only", "store + reports on Postgres", "store + reports on DuckDB service", "store + reports on SQL Server"]
SCENARIO_COLOR = {
    "store only": "var(--aqua)",
    "store + reports on Postgres": "var(--blue)",
    "store + reports on DuckDB service": "var(--orange)",
    "store + reports on SQL Server": "var(--violet)",
}


# ── reading what was measured ────────────────────────────────────────────────

def read_runs(name: str) -> dict[tuple[str, str], list[float]]:
    """engine, question -> the milliseconds of every timed run (the 7-column TSV)."""
    runs: dict[tuple[str, str], list[float]] = defaultdict(list)
    path = RESULTS / name
    if not path.exists():
        return runs
    for row in csv.reader(path.open(), delimiter="\t"):
        if len(row) < 7:
            continue
        runs[(row[2], row[4])].append(float(row[6]))
    return runs


def medians(runs: dict[tuple[str, str], list[float]]) -> dict[tuple[str, str], float]:
    return {key: statistics.median(values) for key, values in runs.items() if values}


def questions_of(runs: dict, model: str) -> list[str]:
    """The questions present, ordered by how long DuckDB took: fastest first."""
    names = {q for (_, q) in runs if q.endswith("." + model)}
    return sorted(names, key=lambda q: statistics.median(runs.get(("duckdb", q), [1e9])))


def read_loadtest_percentiles(name: str) -> dict[tuple[str, str], dict[str, float]]:
    """scenario, operation -> {p50, p95, p99, p999, p9999} from the printed tables."""
    path = RESULTS / name
    out: dict[tuple[str, str], dict[str, float]] = {}
    if not path.exists():
        return out
    scenario = None
    row = re.compile(r"^\s*(product page|order history|checkout|report)\s+([\d,]+)\s+\d+\s+[\d,.]+\s+(.+)$")
    for line in path.read_text().splitlines():
        if line.startswith("Scenario: "):
            scenario = line[len("Scenario: "):].strip()
            continue
        match = row.match(line)
        if not scenario or not match:
            continue
        numbers = [n for n in match.group(3).split() if n not in {"-"}]
        values = [float(n.replace(",", "")) for n in numbers]
        if len(values) < 5:
            continue
        out[(scenario, match.group(1))] = dict(zip(["p50", "p95", "p99", "p999", "p9999"], values))
    return out


def read_samples(name: str, operation: str) -> dict[str, list[float]]:
    """scenario -> every latency of one operation, for a histogram."""
    path = RESULTS / name
    by_scenario: dict[str, list[float]] = defaultdict(list)
    if not path.exists():
        return by_scenario
    names = {"none": "store only", "postgres": "store + reports on Postgres",
             "duckdb": "store + reports on DuckDB service", "sqlserver": "store + reports on SQL Server"}
    for row in csv.DictReader(path.open()):
        if row["operation"] != operation or row["ok"] != "true":
            continue
        by_scenario[names.get(row["scenario"], row["scenario"])].append(float(row["latency_ms"]))
    return by_scenario


# ── drawing ──────────────────────────────────────────────────────────────────

def esc(text: object) -> str:
    return html.escape(str(text))


def tick_label(value: float) -> str:
    """Axis ticks are powers of ten, so they read better short: 1 ms, 10 ms, 1 s, 100 s."""
    return f"{value / 1000:g} s" if value >= 1000 else f"{value:g} ms"


def ms(value: float) -> str:
    if value >= 10_000:
        return f"{value / 1000:,.1f} s"
    if value >= 1000:
        return f"{value / 1000:,.2f} s"
    if value >= 10:
        return f"{value:,.0f} ms"
    return f"{value:,.1f} ms"


class Log:
    """A logarithmic x scale, because the answers run from 6 ms to 138 seconds."""

    def __init__(self, low: float, high: float, width: float, left: float):
        self.low, self.high, self.width, self.left = max(low, 0.05), high, width, left

    def x(self, value: float) -> float:
        value = max(value, self.low)
        span = math.log10(self.high) - math.log10(self.low)
        return self.left + self.width * (math.log10(value) - math.log10(self.low)) / span

    def ticks(self) -> list[tuple[float, str]]:
        first, last = math.floor(math.log10(self.low)), math.ceil(math.log10(self.high))
        out = []
        for power in range(first, last + 1):
            value = 10.0 ** power
            if value < self.low or value > self.high * 1.3:
                continue
            out.append((value, tick_label(value)))
        return out


def grid(scale: Log, top: float, bottom: float) -> str:
    parts = []
    for value, text in scale.ticks():
        x = scale.x(value)
        parts.append(f'<line class="grid" x1="{x:.1f}" y1="{top}" x2="{x:.1f}" y2="{bottom}"/>')
        parts.append(f'<text class="tick" x="{x:.1f}" y="{bottom + 14:.1f}" text-anchor="middle">{esc(text)}</text>')
    return "".join(parts)


def bar_chart(title: str, note: str, rows: list[str], values: dict[tuple[str, str], float], series: list[str],
              label_of: dict[str, str], color_of: dict[str, str], row_label) -> str:
    """Grouped bars, one row per question, one bar per engine, log scale."""
    left, right, top, gap, bar_h = 250, 40, 8, 26, 11
    per_row = len(series) * bar_h + gap
    height = top + per_row * len(rows) + 40
    width = 980
    scale = Log(min([v for v in values.values() if v > 0], default=1) * 0.8,
                max(values.values(), default=1) * 1.15, width - left - right, left)

    parts = [f'<svg viewBox="0 0 {width} {height}" role="img" aria-label="{esc(title)}">']
    parts.append(grid(scale, top, height - 36))
    for index, row in enumerate(rows):
        y0 = top + index * per_row
        parts.append(f'<text class="row" x="{left - 12}" y="{y0 + per_row / 2:.1f}" text-anchor="end">{esc(row_label(row))}</text>')
        for slot, name in enumerate(series):
            value = values.get((name, row))
            y = y0 + slot * bar_h + 2
            if value is None:
                parts.append(f'<text class="missing" x="{left + 4}" y="{y + bar_h - 3:.1f}">did not finish</text>')
                continue
            x = scale.x(value)
            parts.append(
                f'<rect class="bar" x="{left}" y="{y:.1f}" width="{max(x - left, 1):.1f}" height="{bar_h - 3}" '
                f'fill="{color_of[name]}"><title>{esc(label_of[name])} · {esc(row_label(row))}: {esc(ms(value))}</title></rect>')
            parts.append(f'<text class="value" x="{x + 5:.1f}" y="{y + bar_h - 4:.1f}">{esc(ms(value))}</text>')
    parts.append("</svg>")
    return panel(title, note, "".join(parts), legend(series, label_of, color_of))


def candle_chart(title: str, note: str, rows: list[str], runs: dict[tuple[str, str], list[float]],
                 series: list[str], row_label) -> str:
    """Min to max as a wick, the median as a tick: how much the runs of one question differed."""
    left, right, top, gap, lane = 250, 40, 10, 24, 13
    per_row = len(series) * lane + gap
    height = top + per_row * len(rows) + 40
    width = 980
    flat = [v for key, values in runs.items() if key[1] in rows for v in values]
    scale = Log(min(flat, default=1) * 0.8, max(flat, default=1) * 1.15, width - left - right, left)

    parts = [f'<svg viewBox="0 0 {width} {height}" role="img" aria-label="{esc(title)}">']
    parts.append(grid(scale, top, height - 36))
    for index, row in enumerate(rows):
        y0 = top + index * per_row
        parts.append(f'<text class="row" x="{left - 12}" y="{y0 + per_row / 2:.1f}" text-anchor="end">{esc(row_label(row))}</text>')
        for slot, name in enumerate(series):
            values = runs.get((name, row))
            y = y0 + slot * lane + lane / 2
            if not values:
                continue
            low, high, mid = min(values), max(values), statistics.median(values)
            x1, x2, xm = scale.x(low), scale.x(high), scale.x(mid)
            spread = f"{ms(low)} to {ms(high)}" if high > low * 1.005 else "the runs agreed"
            parts.append(f'<line class="wick" x1="{x1:.1f}" y1="{y:.1f}" x2="{max(x2, x1 + 2):.1f}" y2="{y:.1f}" '
                         f'stroke="{COLOR[name]}"><title>{esc(LABEL[name])} · {esc(row_label(row))}: median {esc(ms(mid))}, {esc(spread)}</title></line>')
            parts.append(f'<circle class="median" cx="{xm:.1f}" cy="{y:.1f}" r="4" fill="{COLOR[name]}"/>')
    parts.append("</svg>")
    return panel(title, note, "".join(parts), legend(series, LABEL, COLOR))


def percentile_chart(title: str, note: str, data: dict[tuple[str, str], dict[str, float]], operation: str) -> str:
    """The tail: p50 to p99.99 as a line per scenario, equal steps on x, log ms on y."""
    steps = [("p50", "p50"), ("p95", "p95"), ("p99", "p99"), ("p999", "p99.9"), ("p9999", "p99.99")]
    left, right, top, bottom = 60, 190, 18, 40
    width, height = 980, 330
    flat = [v[key] for (scenario, op), v in data.items() if op == operation for key, _ in steps if v.get(key)]
    if not flat:
        return ""
    low, high = min(flat) * 0.8, max(flat) * 1.2
    plot_w, plot_h = width - left - right, height - top - bottom

    def x_of(i: int) -> float:
        return left + plot_w * i / (len(steps) - 1)

    def y_of(value: float) -> float:
        value = max(value, low)
        span = math.log10(high) - math.log10(low)
        return top + plot_h * (1 - (math.log10(value) - math.log10(low)) / span)

    parts = [f'<svg viewBox="0 0 {width} {height}" role="img" aria-label="{esc(title)}">']
    for power in range(math.floor(math.log10(low)), math.ceil(math.log10(high)) + 1):
        value = 10.0 ** power
        if not low <= value <= high * 1.3:
            continue
        y = y_of(value)
        parts.append(f'<line class="grid" x1="{left}" y1="{y:.1f}" x2="{left + plot_w}" y2="{y:.1f}"/>')
        parts.append(f'<text class="tick" x="{left - 8}" y="{y + 4:.1f}" text-anchor="end">{esc(ms(value))}</text>')
    for i, (_, text) in enumerate(steps):
        parts.append(f'<text class="tick" x="{x_of(i):.1f}" y="{height - 14}" text-anchor="middle">{esc(text)}</text>')

    labels: list[tuple[float, float, str, str]] = []
    for scenario in SCENARIOS:
        values = data.get((scenario, operation))
        if not values:
            continue
        points = [(x_of(i), y_of(values[key])) for i, (key, _) in enumerate(steps) if values.get(key)]
        path = " ".join(f"{'M' if n == 0 else 'L'}{x:.1f},{y:.1f}" for n, (x, y) in enumerate(points))
        parts.append(f'<path class="line" d="{path}" stroke="{SCENARIO_COLOR[scenario]}"/>')
        for (x, y), (key, text) in zip(points, steps):
            parts.append(f'<circle class="dot" cx="{x:.1f}" cy="{y:.1f}" r="4" fill="{SCENARIO_COLOR[scenario]}">'
                         f'<title>{esc(scenario)} · {esc(operation)} {esc(text)}: {esc(ms(values[key]))}</title></circle>')
        last_x, last_y = points[-1]
        labels.append((last_x, last_y, SCENARIO_COLOR[scenario],
                       scenario.replace("store + reports on ", "").replace("store only", "no reports")))

    # Direct labels beat a legend, but only when they do not sit on top of each other: the lines
    # meet at p99.99, so the labels are pushed apart while keeping their order.
    labels.sort(key=lambda item: item[1])
    placed: list[float] = []
    for x, y, color, text in labels:
        while placed and y - placed[-1] < 15:
            y = placed[-1] + 15
        placed.append(y)
        parts.append(f'<text class="series" x="{x + 8:.1f}" y="{y + 4:.1f}" fill="{color}">{esc(text)}</text>')
    parts.append("</svg>")
    return panel(title, note, "".join(parts), "")


def histogram_chart(title: str, note: str, samples: dict[str, list[float]]) -> str:
    """Where the operations actually landed, on log-spaced buckets."""
    flat = [v for values in samples.values() for v in values if v > 0]
    if not flat:
        return ""
    low, high = max(min(flat), 0.05), max(flat)
    buckets = 38
    edges = [10 ** (math.log10(low) + (math.log10(high) - math.log10(low)) * i / buckets) for i in range(buckets + 1)]
    left, right, top, bottom = 60, 180, 14, 40
    width, height = 980, 300
    plot_w, plot_h = width - left - right, height - top - bottom

    counts: dict[str, list[int]] = {}
    for scenario, values in samples.items():
        row = [0] * buckets
        for value in values:
            if value <= 0:
                continue
            index = min(buckets - 1, int((math.log10(max(value, low)) - math.log10(low)) /
                                         (math.log10(high) - math.log10(low)) * buckets))
            row[index] += 1
        counts[scenario] = row
    tallest = max((max(row) for row in counts.values()), default=1)

    parts = [f'<svg viewBox="0 0 {width} {height}" role="img" aria-label="{esc(title)}">']
    for power in range(math.floor(math.log10(low)), math.ceil(math.log10(high)) + 1):
        value = 10.0 ** power
        if not low <= value <= high:
            continue
        x = left + plot_w * (math.log10(value) - math.log10(low)) / (math.log10(high) - math.log10(low))
        parts.append(f'<line class="grid" x1="{x:.1f}" y1="{top}" x2="{x:.1f}" y2="{top + plot_h}"/>')
        parts.append(f'<text class="tick" x="{x:.1f}" y="{height - 14}" text-anchor="middle">{esc(ms(value))}</text>')

    step = plot_w / buckets
    for scenario in SCENARIOS:
        row = counts.get(scenario)
        if not row:
            continue
        points = []
        for i, count in enumerate(row):
            x = left + i * step + step / 2
            points.append((x, top + plot_h * (1 - count / tallest)))
        path = " ".join(f"{'M' if n == 0 else 'L'}{x:.1f},{y:.1f}" for n, (x, y) in enumerate(points))
        parts.append(f'<path class="line thin" d="{path}" stroke="{SCENARIO_COLOR[scenario]}"><title>{esc(scenario)}</title></path>')
    parts.append(f'<text class="tick" x="{left}" y="{top + 10}">← more operations landed here</text>')
    parts.append("</svg>")
    return panel(title, note, "".join(parts), legend(SCENARIOS, {s: s.replace("store + reports on ", "").replace("store only", "no reports") for s in SCENARIOS}, SCENARIO_COLOR))


def legend(series: list[str], label_of: dict[str, str], color_of: dict[str, str]) -> str:
    items = "".join(f'<span class="key"><i style="background:{color_of[name]}"></i>{esc(label_of[name])}</span>'
                    for name in series)
    return f'<div class="legend">{items}</div>'


def panel(title: str, note: str, svg: str, legend_html: str) -> str:
    return (f'<section class="panel"><h3>{esc(title)}</h3><p class="note">{note}</p>'
            f'{legend_html}<div class="plot">{svg}</div></section>')


def table(headers: list[str], rows: list[list[str]], caption: str) -> str:
    head = "".join(f"<th>{esc(h)}</th>" for h in headers)
    body = "".join("<tr>" + "".join(f"<td>{esc(c)}</td>" for c in row) + "</tr>" for row in rows)
    return (f'<details class="numbers"><summary>{esc(caption)}</summary>'
            f'<table><thead><tr>{head}</tr></thead><tbody>{body}</tbody></table></details>')


# ── the page ─────────────────────────────────────────────────────────────────

def build() -> str:
    warm_star = read_runs("bench-star-scale5-laptop.tsv")
    warm_store = read_runs("bench-store-scale5-laptop.tsv")
    cold_star = read_runs("bench-star-scale5-laptop-cold.tsv")
    tuned_store = read_runs("bench-store-scale5-laptop-mssql-tuned.tsv")
    tail = read_loadtest_percentiles("loadtest-scale5-laptop-separate-cores.txt")

    star_questions = questions_of(warm_star, "star")
    store_questions = questions_of(warm_store, "store")
    short = lambda q: q.rsplit(".", 1)[0].replace("-", " ")

    star_medians, store_medians = medians(warm_star), medians(warm_store)
    cold_medians = medians(cold_star)

    parts: list[str] = []

    parts.append(bar_chart(
        "The 15 questions on the star schema",
        "Median of the timed runs, engine-reported time, scale 5. The scale is logarithmic: every gridline is ten times the one before.",
        star_questions, star_medians, ENGINES, LABEL, COLOR, short))
    parts.append(table(["question"] + [LABEL[e] for e in ENGINES],
                       [[short(q)] + [ms(star_medians[(e, q)]) if (e, q) in star_medians else "—" for e in ENGINES]
                        for q in star_questions], "the numbers behind this chart"))

    parts.append(bar_chart(
        "The same questions on the store tables (3NF)",
        "The application's own tables, with the indexes Postgres has and the same ones in SQL Server. pg_duckdb did not finish seven of them within ten minutes.",
        store_questions, store_medians, ENGINES, LABEL, COLOR, short))
    parts.append(table(["question"] + [LABEL[e] for e in ENGINES],
                       [[short(q)] + [ms(store_medians[(e, q)]) if (e, q) in store_medians else "did not finish" for e in ENGINES]
                        for q in store_questions], "the numbers behind this chart"))

    parts.append(candle_chart(
        "How much the runs of one question differed",
        "The line runs from the fastest to the slowest run, the dot is the median. A long line means the machine, not the engine, decided that number.",
        star_questions, warm_star, ENGINES, short))

    cold_rows = [q for q in star_questions if any((e, q) in cold_medians for e in ENGINES)]
    warm_cold = {}
    for q in cold_rows:
        for e in ENGINES:
            if (e, q) in star_medians:
                warm_cold[(f"{e} warm", q)] = star_medians[(e, q)]
            if (e, q) in cold_medians:
                warm_cold[(f"{e} cold", q)] = cold_medians[(e, q)]
    pairs = [f"{e} {state}" for e in ("duckdb", "mssql", "postgres") for state in ("warm", "cold")]
    pair_label = {f"{e} {s}": f"{LABEL[e]}, {s}" for e in ENGINES for s in ("warm", "cold")}
    pair_color = {f"{e} {s}": COLOR[e] for e in ENGINES for s in ("warm", "cold")}
    parts.append(bar_chart(
        "Warm against cold: what each engine's own cache is worth",
        "Cold means the engine's buffer pool was emptied before every timed run (a Postgres restart, DBCC DROPCLEANBUFFERS, a fresh DuckDB process). The operating system's cache stayed warm.",
        cold_rows, warm_cold, pairs, pair_label, pair_color, short))

    for operation in ("product page", "checkout"):
        parts.append(percentile_chart(
            f"The tail of the store: {operation}",
            "p50 to p99.99 of 82 minutes of traffic, 1,200 operations a second, every engine on its own cores. "
            "Past p99.9 the four lines meet: that part is the client and the scheduler, not the database.",
            tail, operation))

    rows = []
    for scenario in SCENARIOS:
        for operation in ("product page", "order history", "checkout"):
            values = tail.get((scenario, operation))
            if values:
                rows.append([scenario, operation] + [ms(values[k]) for k in ("p50", "p95", "p99", "p999", "p9999")])
    parts.append(table(["scenario", "operation", "p50", "p95", "p99", "p99.9", "p99.99"], rows,
                       "every percentile of the long load test"))

    for operation in ("checkout", "product page"):
        samples = read_samples("loadtest-scale5-laptop-sqlserver.csv", operation)
        if samples:
            parts.append(histogram_chart(
                f"Where the {operation} operations landed",
                "One line per scenario over log-spaced buckets, from the shorter 100 operations/s run, which is the one whose every sample was kept. "
                "A second hump to the right is the tail: the same operation, waiting.",
                samples))

    tuned = medians(tuned_store)
    tuned_rows = [q for q in store_questions if ("mssql", q) in tuned]
    tuning = {}
    for q in tuned_rows:
        if ("mssql", q) in store_medians:
            tuning[("plain", q)] = store_medians[("mssql", q)]
        tuning[("tuned", q)] = tuned[("mssql", q)]
    parts.append(bar_chart(
        "SQL Server on the store tables, before and after one tuning script",
        "analytics/mssql-tuning.sql adds a persisted computed column for the order's date, with an index and statistics. "
        "The query text does not change; the plan goes from four nested-loop joins to one hash join.",
        tuned_rows, tuning, ["plain", "tuned"],
        {"plain": "as written", "tuned": "after make docker-mssql-tuning"},
        {"plain": "var(--muted-bar)", "tuned": "var(--violet)"}, short))

    body = "\n".join(parts)
    return PAGE.replace("{{BODY}}", body)


PAGE = """<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>duckstore results</title>
<style>
  :root {
    --bg: #fcfcfb; --fg: #1a1a19; --muted: #5b5b57; --line: #e3e3df; --panel: #ffffff;
    --blue: #2a78d6; --orange: #eb6834; --violet: #4a3aa7; --aqua: #1baf7a; --muted-bar: #9a9a94;
  }
  @media (prefers-color-scheme: dark) {
    :root:not([data-theme="light"]) {
      --bg: #1a1a19; --fg: #f2f2ef; --muted: #a8a8a2; --line: #34342f; --panel: #212120;
      --blue: #3987e5; --orange: #d95926; --violet: #9085e9; --aqua: #199e70; --muted-bar: #6a6a64;
    }
  }
  :root[data-theme="dark"] {
    --bg: #1a1a19; --fg: #f2f2ef; --muted: #a8a8a2; --line: #34342f; --panel: #212120;
    --blue: #3987e5; --orange: #d95926; --violet: #9085e9; --aqua: #199e70; --muted-bar: #6a6a64;
  }
  * { box-sizing: border-box; }
  body { margin: 0; padding: 24px 16px 64px; background: var(--bg); color: var(--fg);
         font: 15px/1.55 ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; }
  main { max-width: 1040px; margin: 0 auto; }
  h1 { font-size: 26px; margin: 0 0 6px; }
  h2 { font-size: 19px; margin: 40px 0 6px; }
  h3 { font-size: 16px; margin: 0 0 4px; }
  p { margin: 0 0 14px; max-width: 78ch; }
  .lede { color: var(--muted); }
  .panel { background: var(--panel); border: 1px solid var(--line); border-radius: 10px; padding: 14px 14px 6px; margin: 14px 0; }
  .note { color: var(--muted); font-size: 13px; margin-bottom: 10px; }
  .plot { overflow-x: auto; }
  svg { width: 100%; height: auto; display: block; }
  .grid { stroke: var(--line); stroke-width: 1; }
  .tick, .row, .value, .series, .missing { fill: var(--muted); font-size: 11px; }
  .row { fill: var(--fg); font-size: 12px; }
  .value { font-size: 10px; }
  .series { font-size: 12px; font-weight: 600; }
  .missing { font-style: italic; }
  .bar { rx: 2; }
  .wick { stroke-width: 3; stroke-linecap: round; }
  .line { fill: none; stroke-width: 2; stroke-linejoin: round; }
  .line.thin { stroke-width: 1.5; }
  .legend { display: flex; flex-wrap: wrap; gap: 14px; margin-bottom: 8px; font-size: 13px; color: var(--fg); }
  .key { display: inline-flex; align-items: center; gap: 6px; }
  .key i { width: 12px; height: 12px; border-radius: 3px; display: inline-block; }
  .numbers { margin: -6px 0 18px; }
  .numbers summary { cursor: pointer; color: var(--muted); font-size: 13px; }
  table { border-collapse: collapse; margin-top: 10px; font-size: 13px; width: 100%; }
  th, td { border-bottom: 1px solid var(--line); padding: 5px 8px; text-align: right; white-space: nowrap; }
  th:first-child, td:first-child, th:nth-child(2), td:nth-child(2) { text-align: left; }
  code { background: var(--line); padding: 1px 5px; border-radius: 4px; font-size: 13px; }
</style>
</head>
<body>
<main>
  <h1>duckstore: the measured results</h1>
  <p class="lede">Every number here was measured on one laptop (AMD Ryzen 7 5825U, 16 threads, 28 GB) at scale 5:
  12.5 million orders, 26.5 million order lines. Each engine reports its own time, the answers were checked against
  Postgres before any timing, and the charts show the median of the runs. Regenerate with
  <code>python3 bench/make-charts.py</code>.</p>
{{BODY}}
  <h2>How to read these</h2>
  <p class="lede">The scales are logarithmic because the answers span five decades, from 6 milliseconds to 138 seconds —
  on a straight scale everything except the slowest bar would be invisible. Every chart has its numbers underneath,
  and every colour means the same engine on every chart.</p>
</main>
</body>
</html>
"""


if __name__ == "__main__":
    OUT.write_text(build())
    print(f"wrote {OUT.relative_to(ROOT)} ({OUT.stat().st_size / 1024:.0f} KB)")
