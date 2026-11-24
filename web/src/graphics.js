import { DATA } from "./data.js";

export const TRACK_COLORS = [
  DATA.tokens.blue,
  DATA.tokens.pink,
  DATA.tokens.lavender,
  DATA.tokens.green
];

export const TRAIL_COLORS = [
  DATA.tokens.blue,
  DATA.tokens.pink,
  DATA.tokens.green,
  DATA.tokens.lavender,
  DATA.tokens.coral,
  DATA.tokens.mint
];

export const STAMP_PALETTE = [
  { id: "0", name: "Ink", hex: DATA.tokens.ink },
  { id: "1", name: "Paper", hex: DATA.tokens.paper },
  { id: "2", name: "Sodium", hex: DATA.tokens.sodium },
  { id: "3", name: "Blue", hex: DATA.tokens.blue },
  { id: "4", name: "Pink", hex: DATA.tokens.pink },
  { id: "5", name: "Green", hex: DATA.tokens.green },
  { id: "6", name: "Lavender", hex: DATA.tokens.lavender },
  { id: "7", name: "Coral", hex: DATA.tokens.coral },
  { id: "8", name: "Mint", hex: DATA.tokens.mint }
];

export function esc(str) {
  return String(str)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

export function hashStr(s) {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return h >>> 0;
}

export function makeRng(seed) {
  let a = seed >>> 0;
  return function () {
    a |= 0;
    a = (a + 0x6D2B79F5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function cubicBezier(x1, y1, x2, y2) {
  return function (t) {
    if (t <= 0) return 0;
    if (t >= 1) return 1;
    let lo = 0, hi = 1, u = t;
    for (let i = 0; i < 12; i++) {
      u = (lo + hi) * 0.5;
      const mu = 1 - u;
      const x = 3 * mu * mu * u * x1 + 3 * mu * u * u * x2 + u * u * u;
      if (x < t) lo = u;
      else hi = u;
    }
    const mu = 1 - u;
    return 3 * mu * mu * u * y1 + 3 * mu * u * u * y2 + u * u * u;
  };
}

export const easeMorph = cubicBezier(0.7, 0, 0.3, 1);
export const easeAssemble = cubicBezier(0.2, 0, 0, 1);

export function layoutGlyphLines(lines, gapRows = 2, useBold = false) {
  const set = useBold ? DATA.glyphsBold : DATA.glyphs;
  let maxCols = 0;
  const cells = [];
  let rowOffset = 0;
  for (let l = 0; l < lines.length; l++) {
    const str = lines[l];
    let colOffset = 0;
    for (let i = 0; i < str.length; i++) {
      const ch = str[i];
      const g = set[ch] || set[" "];
      const w = g[0].length;
      for (let r = 0; r < 9; r++) {
        for (let c = 0; c < w; c++) {
          if (g[r][c] === "#") {
            cells.push({ x: colOffset + c, y: rowOffset + r });
          }
        }
      }
      colOffset += w + (i < str.length - 1 ? 1 : 0);
    }
    if (colOffset > maxCols) maxCols = colOffset;
    rowOffset += 9 + (l < lines.length - 1 ? gapRows : 0);
  }
  return { cols: maxCols, rows: rowOffset, cells };
}

export function svgBitmapText(str, cellH, fill, opts = {}) {
  const layout = layoutGlyphLines(Array.isArray(str) ? str : [str], 2, Boolean(opts.bold));
  const vbW = layout.cols * 5;
  const vbH = layout.rows * 4;
  const color = fill || "currentColor";
  let rects = "";
  if (opts.perCell) {
    for (let i = 0; i < layout.cells.length; i++) {
      const cell = layout.cells[i];
      rects += `<rect x="${cell.x * 5}" y="${cell.y * 4}" width="5" height="4" data-gx="${cell.x}" data-gy="${cell.y}"/>`;
    }
  } else {
    const rowMap = new Map();
    for (let i = 0; i < layout.cells.length; i++) {
      const cell = layout.cells[i];
      if (!rowMap.has(cell.y)) rowMap.set(cell.y, []);
      rowMap.get(cell.y).push(cell.x);
    }
    rowMap.forEach((xs, y) => {
      xs.sort((a, b) => a - b);
      let start = xs[0], prev = xs[0];
      for (let k = 1; k <= xs.length; k++) {
        if (k < xs.length && xs[k] === prev + 1) {
          prev = xs[k];
        } else {
          const span = prev - start + 1;
          rects += `<rect x="${start * 5}" y="${y * 4}" width="${span * 5}" height="4.04"/>`;
          if (k < xs.length) {
            start = xs[k];
            prev = xs[k];
          }
        }
      }
    });
  }
  const wAttr = opts.fluid ? `width="100%"` : `width="${layout.cols * cellH * 1.25}" height="${layout.rows * cellH}"`;
  const cls = opts.className ? ` class="${esc(opts.className)}"` : "";
  const aria = opts.ariaHidden ? ` aria-hidden="true"` : "";
  return `<svg${cls} viewBox="0 0 ${vbW} ${vbH}" ${wAttr} shape-rendering="crispEdges" fill="${color}"${aria}>${rects}</svg>`;
}

export function svgPixelIcon(name, size = 14) {
  const grids = {
    plus: ["..#..","..#..","#####","..#..","..#.."],
    minus: [".....",".....","#####",".....","....."],
    check: ["....#","...#.","#.#..",".#...","....."]
  };
  const g = grids[name] || grids.plus;
  let rects = "";
  for (let y = 0; y < 5; y++) {
    for (let x = 0; x < 5; x++) {
      if (g[y][x] === "#") rects += `<rect x="${x}" y="${y}" width="1" height="1"/>`;
    }
  }
  return `<svg width="${size}" height="${size}" viewBox="0 0 5 5" shape-rendering="crispEdges" fill="currentColor" aria-hidden="true">${rects}</svg>`;
}

// Orthogonal pixel weave generators
export const WEAVES = {
  plus: (x, y, phase) => {
    const px = (x + phase) % 6;
    const py = (y + phase) % 6;
    const inA = (px === 1 && py <= 2) || (py === 1 && px <= 2);
    const inB = (px === 4 && py >= 3) || (py === 4 && px >= 3);
    return (inA || inB) ? 1 : 0;
  },
  cross: (x, y, phase) => {
    const px = (x + phase) % 4;
    const py = (y + phase) % 4;
    return (px === 1 || py === 1) ? 1 : 0;
  },
  tartan: (x, y, phase) => {
    const px = (x + phase) % 4;
    const py = (y + phase) % 4;
    return ((px < 2) !== (py < 2) || (px === 1 && py === 1)) ? 1 : 0;
  },
  basket: (x, y, phase) => {
    const px = (x + phase) % 4;
    const py = (y + phase) % 4;
    const block = ((Math.floor((x + phase) / 4) + Math.floor((y + phase) / 4)) % 2) === 0;
    return block ? (py % 2 === 0 ? 1 : 0) : (px % 2 === 0 ? 1 : 0);
  }
};

// Deterministic 32x32 pixel geometric emblems for the 8 AI clients
export function buildClientEmblemRows(clientId) {
  const rows = [];
  for (let y = 0; y < 32; y++) {
    let row = "";
    for (let x = 0; x < 32; x++) {
      const dx = x - 15.5;
      const dy = y - 15.5;
      const dist = Math.hypot(dx, dy);
      const ax = Math.abs(dx);
      const ay = Math.abs(dy);
      let c = "0";
      if (clientId === "claude") {
        // Radial starburst / spark motif
        const ang = Math.atan2(dy, dx);
        const ray = Math.abs(Math.sin(ang * 4));
        if (dist < 12 && (ray < 0.28 || dist < 4.5)) c = "1";
        if (dist < 13 && dist > 11 && (x + y) % 2 === 0) c = "2";
        if (dist < 2.2) c = "3";
      } else if (clientId === "codex") {
        // Terminal chevron >_ inside crisp frame
        if (ax >= 11 && ax <= 12.5 && ay <= 12.5) c = "2";
        if (ay >= 11 && ay <= 12.5 && ax <= 12.5) c = "2";
        if (x >= 7 && x <= 14 && Math.abs((x - 7) - Math.abs(y - 14)) <= 1.2) c = "1";
        if (y >= 19 && y <= 21 && x >= 15 && x <= 23) c = "3";
      } else if (clientId === "cursor") {
        // Isometric cube / pointer prism
        if (ax + ay <= 12) c = "1";
        if (ax + ay <= 12 && dx > 0 && dy > -ax * 0.4) c = "2";
        if (ax + ay <= 6 && dy < 0) c = "3";
      } else if (clientId === "gemini") {
        // 4-point star diamond
        const star = Math.pow(ax / 12.5, 0.55) + Math.pow(ay / 12.5, 0.55);
        if (star <= 1.0) c = "1";
        if (star <= 0.58) c = "2";
        if (ax < 1.8 && ay < 1.8) c = "3";
      } else if (clientId === "opencode") {
        // Nested brackets [ ] + center node
        if (ay <= 11 && ((x >= 5 && x <= 8) || (x >= 23 && x <= 26))) c = "1";
        if ((y >= 5 && y <= 7 || y >= 24 && y <= 26) && ((x >= 5 && x <= 12) || (x >= 19 && x <= 26))) c = "1";
        if (ax <= 3.5 && ay <= 3.5) c = "2";
        if (ax <= 1.5 && ay <= 1.5) c = "3";
      } else if (clientId === "copilot") {
        // Twin visor goggles
        if (ay <= 7 && ax <= 12 && !(ax < 2 && dy > 2)) c = "1";
        if (ay <= 4 && ((x >= 7 && x <= 12) || (x >= 19 && x <= 24))) c = "2";
        if (ay <= 1.5 && ((x >= 9 && x <= 10) || (x >= 21 && x <= 22))) c = "3";
      } else if (clientId === "windsurf") {
        // Layered pixel waves
        const wave1 = Math.sin(x * 0.35) * 3 + 11;
        const wave2 = Math.sin(x * 0.35 + 1.2) * 3 + 18;
        if (Math.abs(y - wave1) < 2.2) c = "1";
        if (Math.abs(y - wave2) < 2.2) c = "2";
        if (y > 23 && y < 26 && x > 5 && x < 26) c = "3";
      } else {
        // Cline / memory bank stacked blocks
        if (ax <= 11 && ((y >= 5 && y <= 9) || (y >= 13 && y <= 17) || (y >= 21 && y <= 25))) c = "1";
        if (x >= 7 && x <= 10 && (y === 7 || y === 15 || y === 23)) c = "3";
        if (x >= 13 && x <= 24 && (y === 7 || y === 15 || y === 23)) c = "2";
      }
      row += c;
    }
    rows.push(row);
  }
  return rows;
}

// Live 11-pattern Secret Guard scanner (matches internal/guard specification)
export function scanSecrets(text) {
  const findings = [];
  const rules = [
    { name: "Private Key 头", re: /-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----/ },
    { name: "Anthropic Key", re: /\bsk-ant-[A-Za-z0-9_-]{8,}\b/ },
    { name: "OpenAI Key", re: /\bsk-(?:proj-)?[A-Za-z0-9_-]{16,}\b/ },
    { name: "GitHub Token", re: /\bgh[pousr]_[A-Za-z0-9]{16,}\b/ },
    { name: "GitHub Fine-grained PAT", re: /\bgithub_pat_[A-Za-z0-9_]{16,}\b/ },
    { name: "AWS Access Key", re: /\b(?:AKIA|ASIA)[0-9A-Z]{16}\b/ },
    { name: "Google API Key", re: /\bAIza[0-9A-Za-z_-]{20,}\b/ },
    { name: "Slack Token", re: /\bxox[baprs]-[0-9A-Za-z-]{10,}\b/ },
    { name: "Stripe Live Key", re: /\b(?:sk|rk)_live_[0-9A-Za-z]{12,}\b/ },
    { name: "JWT Token", re: /\beyJ[A-Za-z0-9_-]{6,}\.eyJ[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\b/ },
    { name: "带密码数据库/MQ URI", re: /\b(?:postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis|amqps?):\/\/[^:\s/]+:[^@\s/]+@[^\s]+/i }
  ];
  for (const r of rules) {
    if (r.re.test(text)) findings.push(r.name);
  }
  const hasPrivateTag = /<private>[\s\S]*?<\/private>/i.test(text);
  const sanitized = text.replace(/<private>[\s\S]*?<\/private>/gi, "").replace(/\n{3,}/g, "\n\n").trim();
  return { findings, hasPrivateTag, sanitized };
}

export async function copyText(text, btn) {
  const prev = btn ? btn.textContent : "";
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    document.execCommand("copy");
    ta.remove();
  }
  if (btn) {
    btn.textContent = "已复制";
    window.setTimeout(() => {
      btn.textContent = prev;
    }, 1500);
  }
}
