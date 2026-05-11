const tooltip = document.createElement("div");
tooltip.className = "observer-tooltip";
document.body.appendChild(tooltip);

let activeBadge = null;
let refreshInFlight = false;

function applyTileAnimations() {
  const tiles = document.querySelectorAll(".tile");
  tiles.forEach((tile, idx) => {
    tile.style.animationDelay = `${(idx % 10) * 0.13}s`;
  });
}

function placeTooltip(target) {
  const rect = target.getBoundingClientRect();
  const margin = 10;
  const top = Math.max(margin, rect.top - tooltip.offsetHeight - margin);
  const left = Math.min(
    window.innerWidth - tooltip.offsetWidth - margin,
    Math.max(margin, rect.left + rect.width / 2 - tooltip.offsetWidth / 2),
  );
  tooltip.style.top = `${top}px`;
  tooltip.style.left = `${left}px`;
}

function showTooltip(target) {
  const text = target.dataset.tooltip?.trim();
  if (!text) return;
  activeBadge = target;
  tooltip.textContent = text;
  tooltip.classList.add("is-visible");
  placeTooltip(target);
}

function hideTooltip(target) {
  if (!activeBadge || activeBadge !== target) return;
  activeBadge = null;
  tooltip.classList.remove("is-visible");
}

function wireBadgeTooltips() {
  const badges = document.querySelectorAll(".observer-badge[data-tooltip]");
  badges.forEach((badge) => {
    badge.addEventListener("mouseenter", () => showTooltip(badge));
    badge.addEventListener("focus", () => showTooltip(badge));
    badge.addEventListener("mouseleave", () => hideTooltip(badge));
    badge.addEventListener("blur", () => hideTooltip(badge));
  });
}

const LEADERBOARD_FILTER_KEY = "meshmonday-leaderboard-filter";
const LEADERBOARD_GONE_DELAY_MS = 240;

let leaderboardGoneCommitTimer = null;
let leaderboardGoneFlipRaf = 0;

function clearLeaderboardGoneCommitTimer() {
  if (leaderboardGoneCommitTimer) {
    window.clearTimeout(leaderboardGoneCommitTimer);
    leaderboardGoneCommitTimer = null;
  }
  if (leaderboardGoneFlipRaf) {
    window.cancelAnimationFrame(leaderboardGoneFlipRaf);
    leaderboardGoneFlipRaf = 0;
  }
}

function commitLeaderboardGoneRowsWithFlip() {
  const board = document.querySelector(".board");
  if (!board) return;

  const candidates = [
    ...board.querySelectorAll(".board-row.board-row--filtered-out:not(.board-row--gone)"),
  ];
  const toRemove = candidates.filter(
    (row) => parseFloat(getComputedStyle(row).opacity) < 0.05,
  );
  if (toRemove.length === 0) return;

  const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  const survivors = [
    ...board.querySelectorAll(".board-row:not(.board-row--gone)"),
  ].filter((el) => !toRemove.includes(el));

  const beforeRects = new Map(
    survivors.map((el) => [el, el.getBoundingClientRect()]),
  );

  for (const row of toRemove) {
    row.classList.add("board-row--gone");
  }

  if (reduceMotion) return;

  window.requestAnimationFrame(() => {
    for (const el of survivors) {
      if (!el.isConnected || el.classList.contains("board-row--gone")) continue;
      const prev = beforeRects.get(el);
      if (!prev) continue;
      const next = el.getBoundingClientRect();
      const dy = prev.top - next.top;
      if (Math.abs(dy) < 1) continue;

      el.style.transition = "none";
      el.style.transform = `translateY(${dy}px) scale(1)`;
      void el.offsetHeight;
      el.style.transition = "";
      el.style.transform = "translateY(0) scale(1)";

      const done = (e) => {
        if (e.propertyName !== "transform") return;
        el.removeEventListener("transitionend", done);
        el.style.removeProperty("transform");
        el.style.removeProperty("transition");
      };
      el.addEventListener("transitionend", done);
    }
  });
}

function queueLeaderboardGoneCommitFromFade() {
  if (leaderboardGoneFlipRaf) return;
  leaderboardGoneFlipRaf = window.requestAnimationFrame(() => {
    leaderboardGoneFlipRaf = 0;
    commitLeaderboardGoneRowsWithFlip();
  });
}

function wireLeaderboardBoardTransitionEnd() {
  const board = document.querySelector(".board");
  if (!board || board.dataset.lbFadeCommitWired === "1") return;
  board.dataset.lbFadeCommitWired = "1";
  board.addEventListener(
    "transitionend",
    (e) => {
      const row = e.target.closest?.(".board-row");
      if (!row || row !== e.target) return;
      if (e.propertyName !== "opacity") return;
      if (!row.classList.contains("board-row--filtered-out")) return;
      if (row.classList.contains("board-row--gone")) return;
      queueLeaderboardGoneCommitFromFade();
    },
    { passive: true },
  );
}

function wireLeaderboardSearch() {
  const input = document.querySelector("#leaderboard-filter");
  if (!input) return;

  const filterEmpty = document.querySelector(".board-filter-empty");
  clearLeaderboardGoneCommitTimer();
  wireLeaderboardBoardTransitionEnd();

  function normalizeMatchText(value) {
    return String(value ?? "").trim().toLowerCase();
  }

  function normalizeQuery(value) {
    let s = normalizeMatchText(value);
    if (s.startsWith("@")) s = s.slice(1);
    return s;
  }

  function apply() {
    const raw = input.value;
    sessionStorage.setItem(LEADERBOARD_FILTER_KEY, raw);
    const q = normalizeQuery(raw);
    const rowList = document.querySelectorAll(".board-row");
    let visible = 0;
    rowList.forEach((row) => {
      const u = normalizeMatchText(row.getAttribute("data-username"));
      const d = normalizeMatchText(row.getAttribute("data-display-name"));
      const match = !q || u.includes(q) || d.includes(q);
      if (match) {
        row.removeAttribute("aria-hidden");
        row.classList.remove("board-row--gone");
        window.requestAnimationFrame(() => {
          row.classList.remove("board-row--filtered-out");
        });
      } else {
        row.setAttribute("aria-hidden", "true");
        row.classList.add("board-row--filtered-out");
      }
      if (match) visible++;
    });
    if (filterEmpty) {
      const hasRows = rowList.length > 0;
      filterEmpty.hidden = !hasRows || !q || visible > 0;
    }
    clearLeaderboardGoneCommitTimer();
    leaderboardGoneCommitTimer = window.setTimeout(() => {
      leaderboardGoneCommitTimer = null;
      commitLeaderboardGoneRowsWithFlip();
    }, LEADERBOARD_GONE_DELAY_MS);
  }

  input.value = sessionStorage.getItem(LEADERBOARD_FILTER_KEY) ?? "";
  input.addEventListener("input", apply);
  apply();
}

window.addEventListener("scroll", () => {
  if (activeBadge) placeTooltip(activeBadge);
}, { passive: true });

window.addEventListener("resize", () => {
  if (activeBadge) placeTooltip(activeBadge);
});

async function refreshMainInPlace() {
  if (refreshInFlight) return;
  refreshInFlight = true;
  try {
    const response = await fetch(window.location.href, {
      cache: "no-store",
      headers: { "X-Requested-With": "meshmonday-poll" },
    });
    if (!response.ok) return;

    const html = await response.text();
    const parser = new DOMParser();
    const nextDoc = parser.parseFromString(html, "text/html");
    const nextMain = nextDoc.querySelector("main.page");
    const currentMain = document.querySelector("main.page");
    if (!nextMain || !currentMain) return;

    const active = document.activeElement;
    let leaderboardFilterFocus = null;
    if (
      active instanceof HTMLInputElement &&
      active.id === "leaderboard-filter"
    ) {
      leaderboardFilterFocus = {
        start: active.selectionStart,
        end: active.selectionEnd,
        dir: active.selectionDirection,
      };
    }

    tooltip.classList.remove("is-visible");
    activeBadge = null;
    clearLeaderboardGoneCommitTimer();

    currentMain.replaceWith(nextMain);
    if (nextDoc.body?.dataset?.pollSeconds) {
      document.body.dataset.pollSeconds = nextDoc.body.dataset.pollSeconds;
    }
    if (nextDoc.title) {
      document.title = nextDoc.title;
    }
    applyTileAnimations();
    wireBadgeTooltips();
    wireLeaderboardSearch();

    if (leaderboardFilterFocus) {
      const nextInput = document.querySelector("#leaderboard-filter");
      if (nextInput instanceof HTMLInputElement) {
        nextInput.focus();
        const len = nextInput.value.length;
        const s = Math.max(
          0,
          Math.min(
            leaderboardFilterFocus.start ?? len,
            len,
          ),
        );
        const e = Math.max(
          0,
          Math.min(
            leaderboardFilterFocus.end ?? len,
            len,
          ),
        );
        try {
          nextInput.setSelectionRange(
            s,
            e,
            leaderboardFilterFocus.dir || undefined,
          );
        } catch {
          nextInput.setSelectionRange(s, e);
        }
      }
    }
  } catch (_err) {
    // Ignore intermittent polling errors and retry next interval.
  } finally {
    refreshInFlight = false;
  }
}

applyTileAnimations();
wireBadgeTooltips();
wireLeaderboardSearch();

const pollSeconds = Number(document.body?.dataset?.pollSeconds ?? 0);
if (Number.isFinite(pollSeconds) && pollSeconds > 0) {
  window.setInterval(() => {
    if (document.visibilityState === "visible") {
      refreshMainInPlace();
    }
  }, pollSeconds * 1000);
}
