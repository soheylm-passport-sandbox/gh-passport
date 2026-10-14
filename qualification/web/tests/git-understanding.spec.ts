import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { fixtureCurriculum } from "./fixture-curriculum";

for (const width of [1280, 390]) {
  test(`Git understanding questions allow correction without public submission at ${width}px`, async ({ page }) => {
    test.skip(!["4.0.0", "5.0.0", "6.0.0", "7.0.0", "8.0.0", "9.0.0"].includes(fixtureCurriculum), "Existing courses retain their two original questions");
    await page.setViewportSize({ width, height: 844 });
    await page.goto("/missions/git-manual-pr/");
    await expect(page.locator("[data-learning-question]")).toHaveCount(0);
    await expect(page.locator("[data-question]")).toHaveCount(3);
    await expect(page.locator("[data-question] input:checked")).toHaveCount(0);
    await page.locator('[data-question="commit_snapshot"] input[value="saved"]').check();
    await page.locator('[data-question="share_commit"] input[value="published"]').check();
    await page.locator('[data-question="review_correction"] input[value="amend"]').check();
    await page.getByRole("button", { name: "Check sample answers", exact: true }).click();
    await expect(page.getByText("Review the sample feedback", { exact: true })).toBeVisible();
    await expect(page.locator('[data-feedback-for="commit_snapshot"]')).toContainText("A commit records the staged version");
    await page.locator('[data-question="commit_snapshot"] input[value="staged"]').focus();
    await page.keyboard.press("Space");
    await page.getByRole("button", { name: "Check sample answers", exact: true }).click();
    await expect(page.getByText("Sample answers correct", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Submit lesson" })).toBeDisabled();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    const axe = await new AxeBuilder({ page }).analyze();
    expect(axe.violations.filter(v => v.impact === "serious" || v.impact === "critical")).toEqual([]);
  });
}
