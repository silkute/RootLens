import { test, expect } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

test("opt-in process leaders render without command lines",async({page})=>{
  await page.route('**/api/v1/hosts/demo-1/processes',route=>route.fulfill({json:{status:'available',reason:'Names only; no command lines or history.',at:new Date().toISOString(),visible:1,limited:false,processes:[{pid:42,name:'background-worker',cpu_percent:2,rss_bytes:1048576}]}}));
  await page.goto('/#host/demo-1/summary');await expect(page.locator('.loading-state')).toHaveCount(0);
  await page.getByRole('button',{name:'Current process leaders'}).click();
  await expect(page.getByRole('cell',{name:'background-worker',exact:true})).toBeVisible();
  await expect(page.getByRole('cell',{name:'2.0%',exact:true})).toBeVisible();
});

test("overview, filtering, host charts and evidence reports", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "System overview" }),
  ).toBeVisible();
  await page.getByRole("textbox", { name: "Filter systems" }).fill("homelab");
  await expect(page.locator(".systems-table tbody tr")).toHaveCount(1);
  await page
    .getByRole("link", { name: "Open homelab-nas", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "CPU usage", exact: true }),
  ).toBeVisible();
  await expect(page.locator(".uplot").first()).toBeVisible();
  await page
    .getByRole("button", { name: "View data", exact: true })
    .first()
    .click();
  await expect(page.locator(".chart-data table").first()).toBeVisible();
  await page
    .getByRole("link", { name: "Diagnostics", exact: false })
    .first()
    .click();
  await expect(
    page.getByRole("heading", { name: "Evidence, before explanations" }),
  ).toBeVisible();
  await expect(
    page.getByText("Measured fact", { exact: true }).first(),
  ).toBeVisible();
  expect(errors).toEqual([]);
});
for (const theme of ["dark", "light"])
  for (const width of [360, 390, 768, 1440]) {
    test(`${theme} ${width}px has no overflow or serious accessibility violations`, async ({
      page,
    }) => {
      await page.setViewportSize({ width, height: 1000 });
      await page.addInitScript(
        (value) => localStorage.setItem("rootlens-theme", value),
        theme,
      );
      await page.goto("/");
      await expect(
        page.getByRole("heading", { name: "System overview" }),
      ).toBeVisible();
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= window.innerWidth,
        ),
      ).toBeTruthy();
      const report = await new AxeBuilder({ page }).analyze();
      expect(
        report.violations.filter((v) =>
          ["critical", "serious"].includes(v.impact || ""),
        ),
      ).toEqual([]);
      await page.screenshot({
        path: `test-results/overview-${theme}-${width}.png`,
        fullPage: true,
      });
    });
  }
test("all screens remain usable on mobile", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  for (const route of [
    "host/demo-1/summary",
    "host/demo-1/containers",
    "host/demo-1/diagnostics",
    "host/demo-1/timeline",
    "incidents",
    "settings",
  ]) {
    await page.goto(`/#${route}`);
    await expect(page.locator("h1").first()).toBeVisible();
    await expect(page.locator(".loading-state")).toHaveCount(0);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
      route,
    ).toBeTruthy();
  }
});
test("demo refuses mutation and report export returns structured evidence", async ({
  request,
}) => {
  expect(
    (
      await request.patch("/api/v1/settings", { data: { raw_hours: 1 } })
    ).status(),
  ).toBe(403);
  const incidents = await (await request.get("/api/v1/incidents")).json();
  expect(incidents.length).toBeGreaterThan(0);
  const report = await request.get(
    `/api/v1/incidents/${incidents[0].id}/report?format=json`,
  );
  expect(report.ok()).toBeTruthy();
  expect((await report.json()).evidence.length).toBeGreaterThan(0);
});

for (const theme of ["dark", "light"]) {
  test(`${theme} screen accessibility and keyboard navigation`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.addInitScript(
      (value) => localStorage.setItem("rootlens-theme", value),
      theme,
    );
    for (const route of [
      "host/demo-1/summary",
      "host/demo-1/containers",
      "host/demo-1/diagnostics",
      "host/demo-1/timeline",
      "incidents",
      "settings",
    ]) {
      await page.goto(`/#${route}`);
      await expect(page.locator(".loading-state")).toHaveCount(0);
      const report = await new AxeBuilder({ page }).analyze();
      expect(
        report.violations
          .filter((v) => ["critical", "serious"].includes(v.impact || ""))
          .map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })),
        route,
      ).toEqual([]);
    }
    await page.goto("/");
    await expect(
      page.getByRole("heading", { name: "System overview" }),
    ).toBeVisible();
    await page.keyboard.press("Tab");
    await expect(
      page.getByRole("link", { name: "Skip to content" }),
    ).toBeFocused();
    await page.keyboard.press("Enter");
    await expect(page.locator("#main-content")).toBeFocused();
    await expect(
      page.getByRole("heading", { name: "System overview" }),
    ).toBeVisible();
  });
}
