import { expect, test } from "@playwright/test";

test("Git lookup returns to the exact step after reload and browser back", async ({ page }) => {
  const writes: string[] = [];
  page.on("request", request => { if (request.method() !== "GET") writes.push(request.url()); });
  await page.goto("/missions/git-manual-pr/#step-stage");
  await page.locator("#step-stage").getByRole("link", { name: "Optional: exact-file staging explained" }).click();
  await expect(page).toHaveURL(/from=git-manual-pr&at=step-stage#staging-review$/);
  const back = page.locator("[data-reference-return] a");
  await expect(back).toHaveAttribute("href", "/missions/git-manual-pr/#step-stage");
  await expect(back).toContainText("step 6");
  await expect(page.locator("[data-reference-explore]")).toBeHidden();
  await page.reload();
  await expect(back).toBeVisible();
  await page.locator('[id="9-stage-exact-files-and-review-again"]').scrollIntoViewIfNeeded();
  await expect(back).toBeInViewport();
  await back.focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/missions\/git-manual-pr\/#step-stage$/);
  await expect(page.locator("#step-stage")).toBeFocused();
  await page.goBack();
  await expect(back).toBeVisible();
  expect(writes).toEqual([]);
});

test("separate reference tabs do not borrow each other's lesson origin", async ({ page, context }) => {
  await page.goto("/reference/guides/git_workflow/?from=git-manual-pr&at=step-stage");
  const other = await context.newPage();
  await other.goto("/reference/euler/slurm/?from=euler-cpu-first-job&at=lesson-practice");
  await expect(page.locator("[data-reference-return] a")).toHaveAttribute("href", "/missions/git-manual-pr/#step-stage");
  await expect(other.locator("[data-reference-return] a")).toHaveAttribute("href", "/missions/euler-cpu-first-job/#lesson-practice");
  await page.reload();
  await other.reload();
  await expect(page.locator("[data-reference-return] a")).toHaveAttribute("href", "/missions/git-manual-pr/#step-stage");
  await other.close();
});

test("reference origins reject URL injection, unknown anchors and ambiguous parameters", async ({ page }) => {
  for (const query of [
    "from=https%3A%2F%2Fevil.example&at=step-stage",
    "from=git-manual-pr&at=https%3A%2F%2Fevil.example",
    "from=unknown&at=lesson-practice",
    "from=git-manual-pr&at=step-no-such-step",
    "from=git-manual-pr&from=python-environment&at=step-stage",
    "from=git-manual-pr&at=step-stage&setup=computer",
  ]) {
    await page.goto(`/reference/euler/slurm/?${query}`);
    await expect(page.locator("[data-reference-return]")).toBeHidden();
    await expect(page.locator("[data-reference-explore]")).not.toHaveAttribute("hidden");
  }
  await page.goto("/reference/euler/slurm/");
  await expect(page.locator("[data-reference-return]")).toBeHidden();
  await expect(page.locator("[data-reference-progress]")).toHaveAttribute("href", "/getting-started/");
});

test("a second lookup retains the return and computer setup stays setup-only", async ({ page }) => {
  await page.goto("/reference/guides/git_workflow/?from=git-tools-auth&at=lesson-practice&setup=computer");
  const back = page.locator("[data-reference-return] a");
  await expect(back).toHaveAttribute("href", "/missions/git-tools-auth/?setup=computer#lesson-practice");
  const next = page.locator(".reference-markdown a[href^='/core/command-line-basics/']").first();
  const destination = await next.getAttribute("href");
  expect(destination).toContain("from=git-tools-auth");
  expect(destination).toContain("setup=computer");
  await next.click();
  await expect(page.locator("[data-reference-return] a")).toHaveAttribute("href", "/missions/git-tools-auth/?setup=computer#lesson-practice");
});

test("section lookup and plain-text routes work without JavaScript", async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false });
  const page = await context.newPage();
  for (const route of ["/workbook/", "/core/command-line-basics/", "/maintainers/release-process/"]) {
    await page.goto(route);
    await expect(page.locator(".reference-command-primer")).toHaveCount(0);
    await page.getByText("Find a section on this page", { exact: true }).click();
    await expect(page.getByRole("navigation", { name: "Sections on this page" })).toBeVisible();
  }
  await page.goto("/missions/git-manual-pr/#step-stage");
  const lookup = page.locator("#step-stage").getByRole("link", { name: "Optional: exact-file staging explained" });
  await lookup.focus();
  await lookup.press("Enter");
  await expect(page).toHaveURL(/#staging-review$/);
  await expect(page.locator("#staging-review")).toBeAttached();
  await page.goBack();
  await expect(page.locator("#step-stage")).toBeVisible();
  await context.close();
});

test("mobile reference lookup keeps the return, sections and commands reachable", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/reference/euler/slurm/?from=euler-cpu-first-job&at=lesson-practice#completed-job-accounting");
  await expect(page.locator("[data-reference-return] a")).toBeVisible();
  await page.locator("#completed-job-accounting").scrollIntoViewIfNeeded();
  await expect(page.locator("[data-reference-return] a")).toBeInViewport();
  await page.getByText("Find a section on this page", { exact: true }).click();
  await page.getByRole("navigation", { name: "Sections on this page" }).getByRole("link", { name: "Conservative CPU Template" }).click();
  await expect(page.locator("#conservative-cpu-template")).toBeInViewport();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
