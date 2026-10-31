// Run with Node's test runner and Playwright on the module path; see README.md.
// PAI's row on the Agents page behaves like any agent's: the row shows when
// PAI's config is in the faked state (an installed app), its switch connects
// it to magpie (agents/connect) and its model field says what was picked,
// disconnecting asks first and puts the pick back, and a PAI whose config
// isn't seen has no switch. The icon is PAI's own. In English and Chinese;
// no backend, the API is faked here.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const options = [{ value: "gpt-4o-mini", label: "GPT-4o mini" }, { value: "relay/m1", label: "m1", ref: "relay/m1", note: "Relay · via magpie" }];

function server(lang, posts) {
  let wired = false;
  const state = () => ({
    agents: [
      { id: "pai", name: "PAI", icon: "pai", path: "C:\\Users\\me\\AppData\\Roaming\\p-ai\\app_config.toml", wired, fields: [{ key: "model", label: "model", value: wired ? "relay/m1" : "gpt-4o-mini", options }] },
      { id: "goose", name: "Goose", icon: "generic", path: "/fixture/goose", fields: [{ key: "model", label: "model", value: "auto", options: [{ value: "auto", label: "Auto" }] }] },
    ],
    profiles: [],
    settings: { lang, theme: "light" },
  });
  const reply = (route) => route.fulfill({ json: state() });
  return async (route) => {
    const url = new URL(route.request().url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return reply(route);
    const act = url.pathname.match(/^\/api\/agents\/(connect|disconnect)\/(\w+)$/);
    if (act) {
      posts.push(url.pathname);
      wired = act[1] === "connect";
      return reply(route);
    }
    if (url.pathname === "/api/usage/quotas") return json([]);
    if (url.pathname === "/api/groups") return json({ groups: [] });
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname === "/api/agents/cli") return json({ agents: {}, pending: false });
    if (url.pathname === "/api/providers") return json({ providers: [], presets: [], excluded: [], gateway: { running: true, window: true } });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await route.fulfill({ body: await fs.readFile(file), contentType });
  };
}

const words = {
  en: { off: "Not connected · PAI uses its own settings", go: "Disconnect", done: /PAI is connected to magpie/ },
  zh: { go: "断开", done: /PAI 已接入 magpie/ },
};
const row = '.row.agent[data-id="pai"]';

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh"]) {
    const w = words[lang];
    test(`${engine} ${lang}: PAI's row connects, picks a magpie model and disconnects`, async (t) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      t.after(() => browser.close());
      const page = await (await browser.newContext({ viewport: { width: 980, height: 520 }, reducedMotion: "reduce" })).newPage();
      page.setDefaultTimeout(5000);
      page.setDefaultNavigationTimeout(60000);
      const errors = [], posts = [], dialogs = [];
      page.on("pageerror", (e) => errors.push(e.message));
      page.on("dialog", (d) => { dialogs.push(d.message()); d.dismiss(); });
      await page.route("**/*", server(lang, posts));
      await page.goto("http://magpie.test/?view=agents");
      await page.locator(`${row} .ag-conn`).waitFor();

      // PAI's own row is there, with its icon, and its model field reads
      // what its config starts on
      assert.equal(await page.locator(row).count(), 1);
      assert.ok(await page.locator(`${row} .name`).textContent() !== "");
      const tops = () => page.evaluate(() => [document.scrollingElement.scrollTop, document.querySelector("#view-agents")?.scrollTop]);
      const top = await tops();
      const sw = () => page.locator(`${row} .ag-conn`);
      const said = () => page.locator(`${row} .ag-st`).textContent();

      // a config of PAI's has magpie's catalog to pick from, so the switch
      // is there; a Goose fixture with none has none
      assert.equal(await sw().getAttribute("role"), "switch");
      assert.equal(await sw().getAttribute("aria-checked"), "false");
      if (lang === "en") assert.equal(await said(), w.off);
      assert.equal(await page.locator('.row.agent[data-id="goose"] .ag-conn').count(), 0, "the fixture without magpie's models has no switch");

      await sw().click();
      await page.waitForFunction((r) => document.querySelector(r + " .ag-conn")?.getAttribute("aria-checked") === "true", row);
      assert.deepEqual(posts, ["/api/agents/connect/pai"]);
      assert.match(await page.locator("#status").textContent(), w.done);

      // the field now names the picked magpie model
      assert.equal(await page.locator(`${row} .field[data-key="model"] .v`).textContent(), "m1");

      // off asks first, in the app's own dialog
      await sw().click();
      const ask = page.locator(".editor.disconnect-ask");
      await ask.waitFor();
      assert.deepEqual(posts, ["/api/agents/connect/pai"], "switching off asks before it posts");
      await ask.locator(".bar button", { hasText: w.go }).click();
      await ask.waitFor({ state: "detached" });
      await page.waitForFunction((r) => document.querySelector(r + " .ag-conn")?.getAttribute("aria-checked") === "false", row);
      assert.deepEqual(posts, ["/api/agents/connect/pai", "/api/agents/disconnect/pai"]);
      assert.deepEqual(await tops(), top, "no click moved the page");
      assert.deepEqual(dialogs, [], "no native dialog");
      assert.deepEqual(errors, []);
    });
  }
}
