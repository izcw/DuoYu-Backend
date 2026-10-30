import { createServer } from "node:http";
import { readFile, mkdir } from "node:fs/promises";
import path from "node:path";
import assert from "node:assert/strict";
import { chromium } from "@playwright/test";
const root = path.resolve("admin/dist");
const server = createServer(async (req, res) => {
  try {
    let target = path.join(
      root,
      decodeURIComponent(req.url.split("?")[0]).replace(/^\/admin\/?/, ""),
    );
    if (!target.startsWith(root)) throw Error();
    let data;
    try {
      data = await readFile(target);
    } catch {
      target = path.join(root, "index.html");
      data = await readFile(target);
    }
    res.setHeader(
      "Content-Type",
      target.endsWith(".js")
        ? "text/javascript"
        : target.endsWith(".css")
          ? "text/css"
          : target.endsWith(".svg")
            ? "image/svg+xml"
            : target.endsWith(".png")
              ? "image/png"
              : "text/html",
    );
    res.end(data);
  } catch {
    res.writeHead(404);
    res.end();
  }
});
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const base = "http://127.0.0.1:" + server.address().port;
const browser = await chromium.launch({ channel: "msedge", headless: true });
await mkdir("work", { recursive: true });
try {
  const page = await browser.newPage({
    viewport: { width: 1440, height: 1000 },
  });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  let captured = false,
    defaults = false;
  await page.route("**/api/admin/**", async (route) => {
    const req = route.request(),
      url = new URL(req.url()),
      p = url.pathname.slice("/api/admin/".length);
    let data = { items: [] };
    if (p === "me")
      data = {
        account_id: "8888888888",
        csrf: "test",
        role: "super_admin",
        profile: {
          name: "管理员",
          picture: "https://id.duoyu.link/test-avatar.png",
        },
      };
    else if (p === "overview")
      data = {
        users: 28,
        entities: 340,
        storage_bytes: 10485760,
        conflicts: 2,
        operations: 120,
        catalog: 5,
      };
    else if (p === "charts")
      data = {
        items: Array.from({ length: 30 }, (_, i) => ({
          day: "2026-10-" + String(i + 1).padStart(2, "0"),
          users: i % 7,
          operations: 10 + i,
        })),
      };
    else if (p === "users")
      data = {
        items: [
          {
            id: "11111111-1111-4111-8111-111111111111",
            name: "示例用户",
            picture: "https://id.duoyu.link/test-avatar.png",
            subject: "example-user",
            head_revision: "12",
            storage_bytes: "1024",
            disabled: false,
            quota_bytes: null,
            total: 1,
          },
        ],
      };
    else if (p === "templates/capture") {
      captured = true;
      data = { id: "22222222-2222-4222-8222-222222222222" };
    } else if (p === "templates/default") {
      defaults = true;
      data = { ok: true };
    } else if (p.startsWith("catalog/templates"))
      data = {
        items: [
          {
            id: "22222222-2222-4222-8222-222222222222",
            name: "官方模板",
            description: "示例",
            status: "published",
            version: "1",
            payload: { format: "duoyu-template", entities: [] },
            updated_at: new Date().toISOString(),
          },
        ],
      };
    else if (p === "settings")
      data = { items: [{ id: "storage_quota_bytes", value: 104857600 }] };
    await route.fulfill({ json: data });
  });
  await page.route("https://id.duoyu.link/test-avatar.png", (r) =>
    r.fulfill({
      contentType: "image/png",
      body: Buffer.from(
        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jF7sAAAAASUVORK5CYII=",
        "base64",
      ),
    }),
  );
  await page.goto(base + "/admin/");
  await page.getByRole("heading", { name: "概览", exact: true }).waitFor();
  await page.locator("canvas").waitFor();
  assert.equal(
    await page.locator(".dy-header-user .user-avatar").getAttribute("src"),
    "https://id.duoyu.link/test-avatar.png",
  );
  assert.equal(
    await page
      .locator("#app-sidebar")
      .evaluate((el) => Math.round(el.getBoundingClientRect().width)),
    230,
  );
  assert.equal(
    await page
      .locator(".el-menu-item")
      .first()
      .evaluate((el) => el.getBoundingClientRect().height),
    42,
  );
  assert.equal(await page.locator(".el-menu-item svg").count(), 9);
  await page.getByRole("button", { name: "收起菜单", exact: true }).click();
  await page.waitForTimeout(250);
  assert.equal(
    await page
      .locator("#app-sidebar")
      .evaluate((el) => Math.round(el.getBoundingClientRect().width)),
    64,
  );
  await page.getByRole("button", { name: "展开菜单", exact: true }).click();
  await page.waitForTimeout(250);
  await page.screenshot({ path: "work/admin-overview.png", fullPage: true });
  await page.getByRole("menuitem", { name: "用户管理" }).click();
  await page.getByText("示例用户", { exact: true }).waitFor();
  assert.equal(
    await page.locator(".user-identity .user-avatar").getAttribute("src"),
    "https://id.duoyu.link/test-avatar.png",
  );
  await page.getByRole("button", { name: "设为模板", exact: true }).click();
  await page.getByLabel("名称", { exact: true }).fill("新官方模板");
  await page.getByRole("button", { name: "刷新当前页面", exact: true }).click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "取消", exact: true })
    .click();
  assert.equal(
    await page.getByLabel("名称", { exact: true }).inputValue(),
    "新官方模板",
  );
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await page.getByText("用户数据快照已保存").waitFor();
  assert(captured);
  await page.getByRole("menuitem", { name: "模板管理", exact: true }).click();
  await page.getByRole("button", { name: "设为默认", exact: true }).click();
  await page.getByRole("button", { name: "确定", exact: true }).click();
  await page.getByText("已更新官方默认模板").waitFor();
  assert(defaults);
  await page.getByRole("dialog").waitFor({ state: "hidden" });
  await page.screenshot({ path: "work/admin-templates.png", fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(base + "/admin/");
  await page.locator("canvas").waitFor();
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
    "mobile overflow",
  );
  await page.getByRole("button", { name: "打开菜单", exact: true }).click();
  await page.getByRole("menuitem", { name: "用户管理", exact: true }).click();
  await page.getByText("示例用户", { exact: true }).waitFor();
  await page.waitForTimeout(250);
  assert.equal(
    await page
      .locator("#app-sidebar")
      .evaluate((el) => getComputedStyle(el).visibility),
    "hidden",
  );
  await page.getByRole("button", { name: "打开菜单", exact: true }).click();
  await page.getByRole("button", { name: "关闭菜单", exact: true }).click();
  await page.screenshot({ path: "work/admin-mobile.png", fullPage: true });
  assert.deepEqual(errors, []);
  console.log(
    "PASS desktop/mobile admin UI, charts, user snapshot template creation, official-default selection; no page errors",
  );
} finally {
  await browser.close();
  await new Promise((r) => server.close(r));
}
