import { fixtureCurriculum } from "./fixture-curriculum";
import { expect, test } from "@playwright/test";
test("new Slurm review explains the bounded header at the existing inspect steps", async ({ page }) => {
 test.skip(fixtureCurriculum !== "9.0.0", "Earlier courses retain their published instructions");
 for (const width of [1280,390]) {
  await page.setViewportSize({width,height:900});
  for (const [mission,step] of [["euler-cpu-arrays","review"],["euler-gpu-review","review-script"]]) {
   await page.goto(`/missions/${mission}/`);
   const instruction=page.locator(`#step-${step}`);
   await expect(instruction).toContainText("Slurm ignores directives after the first shell command");
   await expect(instruction).toContainText("optional --nodes and --ntasks must each be 1");
   await expect(page.locator("[data-question]")).toHaveCount(3);
   await expect(page.locator("[data-question] input:checked")).toHaveCount(0);
   await expect(page.locator("[data-submit-mission]")).toBeDisabled();
   expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  }
 }
});
