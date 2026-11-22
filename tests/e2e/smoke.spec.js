const { test, expect } = require("@playwright/test");

const BASE = process.env.BASE_URL || "https://rmsradar.ku0hn.radio";

test("deployed UI loads with map tiles and data", async ({ page }) => {
  const consoleErrors = [];
  const badTileResponses = [];

  page.on("console", (m) => {
    if (m.type() === "error") consoleErrors.push(m.text());
  });
  page.on("response", (r) => {
    const u = r.url();
    if (/tile|basemap|cartocdn|openstreetmap/i.test(u) && r.status() >= 400) {
      badTileResponses.push(`${r.status()} ${u}`);
    }
  });

  await page.goto(BASE, { waitUntil: "networkidle", timeout: 60_000 });

  // The app must report that it loaded gateways from the API.
  await expect(page.locator("#status")).toContainText("gateways", { timeout: 30_000 });

  // Keyless tiles must actually load (no "API key required" placeholder/403).
  await expect
    .poll(() => page.locator("img.leaflet-tile-loaded").count(), { timeout: 30_000 })
    .toBeGreaterThan(0);

  // The station list should be populated, with each channel's UTC operating
  // hours visible (24h for all-day channels, otherwise a range such as 06–04 UTC).
  expect(await page.locator("#list li").count()).toBeGreaterThan(0);
  await expect(page.locator(".ch-hours").first()).toContainText(/24h|UTC|\d/);

  // Frequencies default to dial; the radio group exposes Winlink's center value.
  await expect(page.locator("#freq-dial")).toBeChecked();
  await page.locator("#freq-center-label").click();
  await expect(page.locator("#freq-center")).toBeChecked();
  await page.locator("#freq-dial-label").click();
  await expect(page.locator("#freq-dial")).toBeChecked();

  expect(badTileResponses, badTileResponses.join("\n")).toEqual([]);

  // Cloudflare injects its analytics beacon into proxied HTML; our strict CSP
  // blocks it (by design). Ignore only that known third-party script.
  const benign = (m) => /cloudflareinsights\.com|beacon\.min\.js/i.test(m);
  const realErrors = consoleErrors.filter((m) => !benign(m));
  expect(realErrors, realErrors.join("\n")).toEqual([]);
});

test("deployed API endpoints respond", async ({ request }) => {
  for (const p of ["/api/health", "/api/config", "/api/servicecodes", "/api/conditions", "/api/gateways"]) {
    const res = await request.get(BASE + p);
    expect(res.ok(), `${p} -> ${res.status()}`).toBeTruthy();
  }
  const health = await (await request.get(BASE + "/api/health")).json();
  expect(health.ok).toBe(true);
});

test("client setup guide is served", async ({ page }) => {
  const res = await page.goto(BASE + "/clients.html", { waitUntil: "domcontentloaded" });
  expect(res.ok()).toBeTruthy();
  await expect(page.locator("h1")).toContainText("Configuring service codes");
  for (const name of ["Winlink Express", "PAT", "WoAD", "RadioMail", "HTCommander"]) {
    await expect(page.locator("h2", { hasText: name })).toHaveCount(1);
  }
});

test("mobile map/list tabs with a scrollable list", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(BASE, { waitUntil: "networkidle", timeout: 60_000 });

  // The map is the default view and has usable height.
  await expect(page.locator("#tab-map")).toBeVisible();
  await expect(page.locator("#tab-list")).toBeVisible();
  await expect(page.locator("#map")).toBeVisible();
  await expect(page.locator("#list-panel")).toBeHidden();
  const mapBox = await page.locator("#map").boundingBox();
  expect(mapBox.height).toBeGreaterThan(200);

  // Switch to the list: it fills the view and scrolls internally.
  await page.locator("#tab-list").click();
  await expect(page.locator("#list-panel")).toBeVisible();
  await expect(page.locator("#map")).toBeHidden();
  const scrollable = await page.locator("#list").evaluate((el) => el.scrollHeight > el.clientHeight);
  expect(scrollable).toBeTruthy();

  // Picking a station jumps back to the map and opens its info popup. Choose
  // the target from the live data instead of hardcoding a callsign.
  const stations = await (await request.get(BASE + "/api/gateways")).json();
  const target = stations.find((s) => s.latitude && s.longitude && s.channels && s.channels.length);
  expect(target, "expected at least one located gateway").toBeTruthy();
  await page.locator("#list li", { hasText: target.callsign }).first().click();
  await expect(page.locator("#map")).toBeVisible();
  await expect(page.locator("#list-panel")).toBeHidden();
  await expect(page.locator(".leaflet-popup-content")).toContainText(target.callsign);
});

test("gateway setup guide is served", async ({ page }) => {
  const res = await page.goto(BASE + "/gateway.html", { waitUntil: "domcontentloaded" });
  expect(res.ok()).toBeTruthy();
  await expect(page.locator("h1")).toContainText("Setting up a Winlink gateway");
  await expect(page.locator("h2", { hasText: "RMS Trimode" })).toHaveCount(1);
  await expect(page.locator("h2", { hasText: "BPQ32" })).toHaveCount(1);
});

test("a superseded rank does not repopulate the list", async ({ page }) => {
  await page.route("**/api/rank", async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 2500));
    try {
      await route.continue();
    } catch (_) {
      // The request was aborted by the filter change; nothing to continue.
    }
  });
  await page.goto(BASE, { waitUntil: "networkidle", timeout: 60_000 });
  await page.fill("#grid", "EN43bx");
  await page.click("#rank-btn");
  await page.waitForTimeout(150);
  // Changing a filter clears the rank and aborts the in-flight request.
  await page.selectOption("#band", { index: 1 });
  await page.waitForTimeout(3500);
  expect(await page.locator(".q-badge").count()).toBe(0);
});
