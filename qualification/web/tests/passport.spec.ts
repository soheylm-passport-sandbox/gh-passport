import { fixtureCurriculum } from "./fixture-curriculum";
import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Locator, type Page } from "@playwright/test";
import { readFileSync } from "node:fs";

const gitMissionSource = () => {
  if (["7.0.0", "8.0.0", "9.0.0"].includes(fixtureCurriculum)) {
    return JSON.parse(readFileSync(new URL("../../curriculum/missions/git-manual-pr/mission.json", import.meta.url), "utf8"));
  }
  return JSON.parse(readFileSync(new URL(`../../curriculum/compatibility/${fixtureCurriculum}.json`, import.meta.url), "utf8")).missions["git-manual-pr"].metadata;
};

const agentContract = JSON.parse(readFileSync(
  new URL("../../passport_system/agent_contract.json", import.meta.url), "utf8",
)) as { rules: Array<{ label: string; message: string }> };

const assertAccessible = async (page: Page) => {
  const results = await new AxeBuilder({ page }).analyze();
  const serious = results.violations.filter((item) =>
    item.impact === "serious" || item.impact === "critical"
  );
  expect(serious, JSON.stringify(serious, null, 2)).toEqual([]);
};

const localContext = (overrides: Record<string, unknown> = {}) => ({
  mode: "local",
  state_source: "local_navigation_only",
  repository: {
    owner: "student",
    name: "passport-exercises",
    upstream_owner: "soheylm-passport-sandbox",
    upstream_name: "passport-exercises",
    branch: "onboarding/student",
    head_sha: "a".repeat(40),
    remote_head_sha: "b".repeat(40),
    dirty: false,
    passport: {
      schema_version: 2,
      curriculum_version: fixtureCurriculum,
      github_user: "student",
      source_repository: "soheylm-passport-sandbox/passport-exercises",
      fork_repository: "student/passport-exercises",
      platform: "linux",
      responsibilities: ["git"],
      assessment_branch: "onboarding/student",
      missions: ["core-orientation", "core-accounts-secrets"],
      setup_complete: true,
    },
  },
  local_state: {
    schema_version: 1,
    passport_id: "opaque",
    curriculum_version: fixtureCurriculum,
    route_digest: "digest",
    last_opened_mission: "core-accounts-secrets",
    expanded_help: [],
    mission_drafts: {},
    attempt_counts: {},
    pending_submissions: {},
    launch_count: 1,
  },
  ...overrides,
});

test("Euler GitHub sync stays an optional guide outside the assessed lesson", async ({ page }) => {
  await page.goto("/missions/euler-storage-reproducibility/");
  const card = page.locator("[data-optional-euler-sync]");
  await expect(card).toBeVisible();
  await expect(card).toContainText("optional");
  await expect(card).toContainText("does not change Passport progress");
  const link = card.getByRole("link", { name: "Open the code sync guide" });
  await expect(link).toHaveAttribute("href", /\/reference\/euler\/github-code-sync\/\?from=euler-storage-reproducibility&at=lesson-practice$/);
  await link.click();
  await expect(page.getByRole("heading", { name: "Move Project Code Between Your Computer, GitHub, And Euler" })).toBeVisible();
  await expect(page.locator("pre").filter({ hasText: "gh repo create passport-euler-sync-demo --private --add-readme --clone" })).toBeVisible();
  await expect(page.locator("pre").filter({ hasText: "git pull --ff-only" })).toBeVisible();
  await page.goto("/missions/euler-cpu-first-job/");
  await expect(page.locator("[data-optional-euler-sync]")).toHaveCount(0);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/missions/euler-storage-reproducibility/");
  await expect(page.locator("[data-optional-euler-sync] a")).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

const copyViewports = [
  { name: "desktop", width: 1280, height: 900, zoom: 1 },
  { name: "mobile", width: 390, height: 844, zoom: 1 },
  { name: "narrow", width: 320, height: 740, zoom: 1 },
  { name: "200% CSS zoom", width: 1280, height: 900, zoom: 2 },
];

// Visibility alone does not detect a control clipped by a scrolling ancestor.
const expectReachable = async (control: Locator) => {
  await expect(control).toBeVisible();
  const geometry = await control.evaluate((element) => {
    const rect = element.getBoundingClientRect();
    const hit = document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2);
    // Firefox at CSS zoom reports an edge 0.00003px beyond the viewport.
    // Ignore only floating-point noise; clipping by a visible pixel still fails.
    const precision = 0.001;
    const reachable = rect.width > 0 && rect.height > 0
      && rect.left >= -precision && rect.right <= innerWidth + precision
      && rect.top >= -precision && rect.bottom <= innerHeight + precision
      && Boolean(hit && element.contains(hit));
    return { reachable, rect: rect.toJSON(), viewport: [innerWidth, innerHeight], hit: hit?.tagName };
  });
  expect(geometry.reachable, JSON.stringify(geometry)).toBe(true);
};

// Scroll as a learner would before selecting a control on the long Git page.
// Normal hit-testing and check() remain required; never force or set its value.
const centerControl = async (control: Locator) => {
  // Resize or feedback can reflow the page just after scrolling. Retry the
  // scroll with strict hit testing; clipping still fails after 5 seconds.
  await control.evaluate(element => element.scrollIntoView({ behavior: "instant", block: "center" }));
  await expect(async () => {
    const box = await control.boundingBox();
    const viewport = control.page().viewportSize();
    if (box && viewport && (box.y < 0 || box.y + box.height > viewport.height)) {
      // Intel WebKit at CSS zoom can leave scrollIntoView below the viewport.
      // Wheel input follows the learner's scroll; strict hit testing still gates it.
      await control.page().mouse.move(Math.max(1, Math.min(viewport.width - 1, box.x + box.width / 2)), viewport.height / 2);
      await control.page().mouse.wheel(0, (box.y + box.height / 2 - viewport.height / 2) / 2);
    }
    await expectReachable(control);
  }).toPass({ timeout: 5_000 });
};
const checkVisible = async (control: Locator) => {
  await centerControl(control);
  await control.check();
};

const checkScrolledCopy = async (page: Page, card: Locator, expected: string) => {
  const pre = card.locator("pre");
  const button = card.locator("[data-copy-command]");
  await expect(card.locator("code")).toHaveText(expected, { useInnerText: false });
  expect(await pre.evaluate((element) => element.scrollWidth > element.clientWidth)).toBe(true);
  // Center before testing horizontal scrolling: nearest-edge scrolling can
  // leave a fraction of a CSS pixel beyond the viewport after rounding.
  await centerControl(button);
  const original = (await button.boundingBox())!;
  for (const end of [1, 0]) {
    await pre.evaluate((element, end) => { element.scrollLeft = end * element.scrollWidth; }, end);
    expect(await pre.evaluate((element) => element.scrollLeft > 0)).toBe(Boolean(end));
    await expectReachable(button);
    const position = (await button.boundingBox())!;
    expect(Math.abs(position.x - original.x)).toBeLessThan(1);
    // Firefox can move the button by one pixel within an unchanged toolbar.
    // Check containment and reachability rather than an absolute Y snapshot.
    const heading = (await card.locator(".command-heading").boundingBox())!;
    expect(position.y).toBeGreaterThanOrEqual(heading.y);
    expect(position.y + position.height).toBeLessThanOrEqual(heading.y + heading.height);
    // The entire Copy control stays above the code, without covering its text.
    expect(position.y + position.height).toBeLessThanOrEqual((await pre.boundingBox())!.y);
    await button.click();
    await expect.poll(() => page.evaluate(() => sessionStorage.getItem("test-copied-command"))).toBe(expected);
    await expect(button).toHaveText("Copied");
    await expect(button).toHaveText("Copy");
  }
  // Both the code scroller and Copy can receive focus; Enter activates Copy.
  // Native Tab traversal also depends on the browser/OS keyboard preferences.
  await pre.focus();
  await expect(pre).toBeFocused();
  await button.focus();
  await expect(button).toBeFocused();
  await page.evaluate(() => sessionStorage.removeItem("test-copied-command"));
  await page.keyboard.press("Enter");
  await expect.poll(() => page.evaluate(() => sessionStorage.getItem("test-copied-command"))).toBe(expected);
};

test.beforeEach(async ({ page }) => {
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
});

test("front door presents one clear start and remains accessible", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("[data-runtime-label]")).toHaveText("Public course preview");
  await expect(page.getByRole("heading", { level: 1 })).toContainText("Follow one lesson");
  await expect(page.getByRole("link", { name: "How to get your passport" })).toBeVisible();
  await expect(page.getByText(/Your real passport runs on your computer and publishes only exercise results/)).toBeVisible();
  await assertAccessible(page);
});

test("bootstrap creates nothing before explicit public-record consent", async ({ page }) => {
  let setupCalls = 0;
  await page.route("**/__passport/v1/context", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify({ mode: "bootstrap", detected_platform: "windows" }),
  }));
  await page.route("**/__passport/v2/setup", async (route) => {
    setupCalls += 1;
    expect(route.request().postDataJSON()).toEqual({
      platform: "windows",
      responsibilities: ["git"],
      public_record_consent: true,
    });
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({ status: "ready", next: "/passport/" }),
    });
  });
  await page.goto("/start/");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Create your Passport safely.");
  await expect(page.getByText("No public exercise copy or submission record exists yet.")).toBeVisible();
  await page.getByLabel(/Modify or review files through GitHub/).check();
  await page.getByRole("button", { name: "Create my Passport" }).click();
  await expect.poll(() => setupCalls).toBe(0);
  await expect(page.getByText("Confirm what will be public before continuing.")).toBeVisible();
  await page.getByLabel(/I understand what will be public/).check();
  await page.getByRole("button", { name: "Create my Passport" }).click();
  await expect(page).toHaveURL(/\/passport\/$/);
  expect(setupCalls).toBe(1);
});

test("lesson chooser uses the dependency-expanded Euler GPU plan", async ({ page }) => {
  await page.goto("/start/");
  await page.getByLabel("Linux").check();
  await page.getByLabel(/Run GPU computation on Euler/).check();
  await page.getByRole("button", { name: "Show sample lesson list" }).click();
  const result = page.locator(".route-result");
  await expect(result).toBeVisible();
  await expect(result.getByRole("heading", { level: 2 })).toContainText("11 lessons");
  await expect(result).toContainText(["8.0.0", "9.0.0"].includes(fixtureCurriculum) ? "5h 10m" : "5h 25m");
  await expect(result.getByRole("link", { name: /Review A One-GPU Euler Plan/ })).toBeVisible();
  await page.reload();
  await expect(page.getByLabel(/Run GPU computation on Euler/)).toBeChecked();
  await expect(result).toBeVisible();
  await assertAccessible(page);
});

test("lesson chooser detects the computer but preserves a saved choice", async ({ page }) => {
  let releaseRouteData = () => {};
  const routeDataReleased = new Promise<void>((resolve) => {
    releaseRouteData = resolve;
  });
  await page.route("**/data/routes.json", async (route) => {
    await routeDataReleased;
    await route.continue();
  });
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "platform", { configurable: true, get: () => "MacIntel" });
    Object.defineProperty(navigator, "userAgentData", {
      configurable: true,
      get: () => ({ platform: "macOS" }),
    });
  });
  await page.goto("/start/");
  await expect(page.getByRole("radio", { name: "macOS", exact: true })).toBeChecked();

  await page.getByRole("radio", { name: "Windows", exact: true }).check();
  await page.getByRole("button", { name: "Show sample lesson list" }).click();
  await expect.poll(() => page.evaluate((version) => {
    const saved = JSON.parse(localStorage.getItem(`ideal-passport-route:${version}`) || "null");
    return saved?.platform;
  }, fixtureCurriculum)).toBe("windows");
  releaseRouteData();
  await expect(page.locator(".route-result")).toBeVisible();
  await page.reload();
  await expect(page.getByRole("radio", { name: "Windows", exact: true })).toBeChecked();
});

test("a real incident leaves the exercise without collecting details", async ({ page }) => {
  await page.goto("/missions/core-incidents-ai-responsibility/");
  const exit = page.getByRole("link", { name: "This is a real incident" });
  await expect(exit).toBeVisible();
  await expect(exit).toHaveAttribute("href", /\/core\/incidents-and-help\/$/);
  await expect(page.getByText(/Do not put real names, logs, credentials/)).toBeVisible();
});

test("public lesson previews allow question practice without submission", async ({ page }) => {
  await page.goto("/missions/core-orientation/");
  const firstAnswer = page.getByLabel("In a PowerShell window, after its prompt; then press Enter.");
  await expect(firstAnswer).toBeEnabled();
  await expect(page.getByRole("button", { name: "Check sample answers" })).toBeEnabled();
  await expect(page.getByRole("button", { name: "Submit lesson" })).toBeDisabled();

  await firstAnswer.check();
  await page.getByLabel("The automatic Passport check shown on GitHub for the submitted work.").check();
  await page.getByLabel("Run gh passport open; the launcher remembers the managed folder on this computer.").check();
  await page.getByRole("button", { name: "Check sample answers" }).click();

  await expect(page.getByText("Sample answers correct", { exact: true })).toBeVisible();
  await expect(page.getByText(/practice result is not stored and is not course completion/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Submit lesson" })).toBeDisabled();
});

test("lesson preview does not overlap its introduction after scrolling", async ({ page }) => {
  await page.setViewportSize({ width: 1556, height: 900 });
  await page.goto("/start/");
  await page.getByRole("button", { name: "Show sample lesson list" }).click();
  await page.locator(".route-result").scrollIntoViewIfNeeded();
  const boxes = await page.evaluate(() => {
    const copy = document.querySelector(".route-builder-copy")?.getBoundingClientRect();
    const result = document.querySelector(".route-result")?.getBoundingClientRect();
    if (!copy || !result) return null;
    return {
      copy: { left: copy.left, right: copy.right, top: copy.top, bottom: copy.bottom },
      result: { left: result.left, right: result.right, top: result.top, bottom: result.bottom },
    };
  });
  expect(boxes).not.toBeNull();
  const overlaps = boxes!.copy.left < boxes!.result.right
    && boxes!.copy.right > boxes!.result.left
    && boxes!.copy.top < boxes!.result.bottom
    && boxes!.copy.bottom > boxes!.result.top;
  expect(overlaps).toBe(false);
});

test("track introductions do not add duplicate punctuation", async ({ page }) => {
  await page.goto("/tracks/euler-cpu/");
  await expect(page.locator(".track-hero > div > p").nth(1)).not.toContainText("..");
});

test("getting-started commands do not overlap nearby content", async ({ page }) => {
  for (const width of [390, 800, 1024, 1180, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    await page.goto("/getting-started/");
    const collisions = await page.evaluate(() => {
      const overlaps = (left: DOMRect, right: DOMRect) => left.left < right.right
        && left.right > right.left
        && left.top < right.bottom
        && left.bottom > right.top;
      const selectors = [
        ".command-platform-choice",
        ".terminal-openers article:not([hidden])",
        ".terminal-practice",
        ".prerequisite-checks > div",
        ".platform-installer article:not([hidden])",
      ];
      return selectors.flatMap((selector) => Array.from(document.querySelectorAll(selector)).flatMap((container) => {
        const children = Array.from(container.children).filter((child) => !child.hasAttribute("hidden"));
        const results: string[] = [];
        for (let leftIndex = 0; leftIndex < children.length; leftIndex += 1) {
          for (let rightIndex = leftIndex + 1; rightIndex < children.length; rightIndex += 1) {
            if (overlaps(children[leftIndex].getBoundingClientRect(), children[rightIndex].getBoundingClientRect())) {
              results.push(`${selector}: ${children[leftIndex].tagName} overlaps ${children[rightIndex].tagName}`);
            }
          }
        }
        return results;
      }));
    });
    expect(collisions, `${width}px: ${collisions.join(", ")}`).toEqual([]);
  }
});

test("getting-started explains the terminal before the first command", async ({ page }) => {
  await page.goto("/getting-started/");
  await expect(page.getByRole("heading", { name: "What is a terminal, shell, and command?" })).toBeVisible();
  await expect(page.getByText(/do not need to find or install a separate Bash or zsh application/)).toBeVisible();
  await expect(page.locator(".terminal-anatomy")).toContainText("git --version");
  await expect(page.locator(".terminal-anatomy")).toContainText("Prompt");
  await expect(page.getByText(/Windows, macOS, and Linux are operating systems/)).toBeVisible();
  await page.locator(".start-vocabulary summary").press("Enter");
  await expect(page.getByText(/CLI means command-line interface/)).toBeVisible();
  const chooser = page.locator(".command-platform-choice");
  await chooser.getByRole("tab", { name: "Windows", exact: true }).click();
  await expect(page.locator(".terminal-openers").getByRole("heading", { name: "Open PowerShell" })).toBeVisible();
  await expect(page.locator(".terminal-practice").getByText('Write-Output "terminal-ready"')).toBeVisible();
  await chooser.getByRole("tab", { name: "macOS", exact: true }).click();
  await expect(page.locator('.terminal-openers [data-platform-panel="macos"]')).toContainText("starts the zsh shell");
  await expect(page.locator('.terminal-practice [data-platform-panel="macos"]')).toContainText("terminal-ready");
  await chooser.getByRole("tab", { name: "Linux", exact: true }).click();
  await expect(page.locator('.terminal-openers [data-platform-panel="linux"]')).toContainText("starts the Bash shell");
  await expect(page.getByText(/do not search for a separate Bash app/)).toBeVisible();
  await expect(page.locator('.terminal-practice [data-platform-panel="linux"]')).toContainText("terminal-ready");
  const order = await page.evaluate(() => {
    const explanation = document.querySelector(".command-basics")?.getBoundingClientRect();
    const firstCommand = document.querySelector(".prerequisite-checks")?.getBoundingClientRect();
    return explanation && firstCommand ? explanation.bottom <= firstCommand.top : false;
  });
  expect(order).toBe(true);
});

test("getting-started recovery panels stay in the instruction column", async ({ page }) => {
  for (const width of [390, 780]) {
    await page.setViewportSize({ width, height: 900 });
    await page.goto("/getting-started/");
    const alignment = await page.locator(".bootstrap-sequence > ol > li").evaluateAll((steps) => steps.map((step) => {
      const instruction = step.querySelector(":scope > div")?.getBoundingClientRect();
      const recovery = step.querySelector(":scope > .bootstrap-recovery")?.getBoundingClientRect();
      return instruction && recovery
        ? { leftDifference: Math.abs(instruction.left - recovery.left), recoveryWidth: recovery.width }
        : null;
    }));
    expect(alignment.every((item) => item !== null && item.leftDifference < 1 && item.recoveryWidth > 180)).toBe(true);
  }
});

test("mission stays readable and navigable without JavaScript", async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false });
  const page = await context.newPage();
  await page.goto("/missions/core-orientation/");
  await expect(page.getByRole("heading", { level: 1 })).toContainText("Start And Resume Your Passport");
  await expect(page.getByRole("heading", { name: "Work through the decisions" })).toBeVisible();
  await expect(page.locator("#step-resume")).toContainText("the browser reopens the same Passport and lesson list");
  await expect(page.locator(".learning-challenge")).toContainText("Work you can resume; no official pass");
  const support = page.locator(".mission-support");
  await expect(support.getByText("Need help?", { exact: true })).toBeVisible();
  await support.locator("summary").click();
  await expect(support.getByRole("heading", { name: "If Blocked" })).toBeVisible();
  await expect(page.getByText("Course preview only", { exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "Skip to main content" })).toBeAttached();
  await context.close();
});

test("mission explanations stay open with one keyboard-accessible terminal refresher", async ({ page }) => {
  await page.goto("/missions/git-tools-auth/");
  await expect(page.locator(".mission-command-primer")).toHaveCount(0);
  await expect(page.locator(".learning-brief")).toBeVisible();
  await expect(page.locator(".learning-brief")).toContainText("Git is version-control software: it records file versions.");
  await expect(page.locator(".learning-brief").getByRole("table")).toBeVisible();
  await expect(page.locator(".learning-brief").getByRole("heading", { name: "If Blocked" })).toHaveCount(0);
  await expect(page.locator(".mission-support").getByText("Need help?", { exact: true })).toBeVisible();
  const guide = page.locator(".recipe-command-guide");
  await expect(guide).toHaveCount(1);
  await expect(guide).not.toHaveAttribute("open", "");
  await expect(guide.locator(".recipe-command-guide-content")).toBeHidden();
  await guide.locator("summary").focus();
  await page.keyboard.press("Enter");
  await expect(guide).toHaveAttribute("open", "");
  await expect(guide.getByRole("link", { name: "Practise one harmless command first" })).toHaveAttribute("href", /\/getting-started\/#command-basics-title$/);
  await expect(guide).toContainText("A command is a line of text that tells a computer to do one task");
  await expect(guide).toContainText("A shell is the part inside that application");
  await expect(guide).toContainText("do not need to install a separate Bash or zsh application");
  await expect(guide).toContainText("press the Windows key");
  await expect(guide).toContainText("Command+Space");
  await expect(guide).toContainText("applications menu");
  await page.getByRole("button", { name: "macOS" }).click();
  await expect(guide.locator('[data-command-guide-platform="macos"]')).toBeVisible();
  await expect(guide.locator('[data-command-guide-platform="windows"]')).toBeHidden();
  await expect(guide.locator('[data-command-guide-platform="linux"]')).toBeHidden();
  await expect(guide.getByRole("link", { name: "How to open a terminal and run a command" })).toHaveAttribute("href", "/core/command-line-basics/?from=git-tools-auth&at=lesson-practice");
  await expect(page.getByRole("button", { name: "Prepare practice folder" })).toBeDisabled();
  await expect(page.getByText("This public sample cannot create files. Open your local Passport to use this button.")).toBeVisible();
  await assertAccessible(page);
});

test("current Git lesson prepares its practice folder and shows the next command", async ({ page }) => {
  const base = localContext();
  const context = localContext({
    repository: {
      ...base.repository,
      passport: {
        ...base.repository.passport,
        platform: "windows",
        missions: ["git-tools-auth"],
      },
    },
    local_state: {
      ...base.local_state,
      last_opened_mission: "git-tools-auth",
    },
  });
  let practiceCalls = 0;
  let releasePractice = () => {};
  const practiceResponse = new Promise<void>((resolve) => { releasePractice = resolve; });
  await page.route("**/__passport/v1/context", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify(context),
  }));
  await page.route("**/__passport/v2/practice", async (route) => {
    practiceCalls += 1;
    expect(route.request().method()).toBe("POST");
    await practiceResponse;
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        status: "ready",
        path: "C:\\Users\\student\\IDEAL-passport\\practice",
        branch: "practice/student",
      }),
    });
  });
  await page.goto("/missions/git-tools-auth/");
  const button = page.getByRole("button", { name: "Prepare practice folder" });
  await expect(button).toBeEnabled();
  await expect(page.getByText("Ready. Select the button once; the launcher handles the Git setup.")).toBeVisible();
  await button.click();
  await expect(page.getByText("Creating or refreshing the practice folder. Keep this page open.")).toBeVisible();
  releasePractice();
  await expect(page.getByRole("button", { name: "Practice folder ready" })).toBeDisabled();
  await expect(page.getByText("Folder: C:\\Users\\student\\IDEAL-passport\\practice")).toBeVisible();
  await expect(page.locator("[data-practice-commands] code")).toHaveText('Set-Location -LiteralPath "C:\\Users\\student\\IDEAL-passport\\practice"');
  expect(practiceCalls).toBe(1);
});

test("manual PR lesson names the learner fork and branch explicitly", async ({ page }) => {
  const base = localContext();
  const context = localContext({
    repository: {
      ...base.repository,
      owner: "IDEALLabXP",
      passport: {
        ...base.repository.passport,
        github_user: "IDEALLabXP",
        fork_repository: "IDEALLabXP/passport-exercises",
        missions: ["git-manual-pr"],
      },
    },
    local_state: {
      ...base.local_state,
      last_opened_mission: "git-manual-pr",
    },
  });
  await page.route("**/__passport/v1/context", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify(context),
  }));
  await page.goto("/missions/git-manual-pr/");
  const finalReview = page.locator('[data-recipe-step]').filter({ hasText: "Review what GitHub received" });
  await expect(finalReview).toContainText("Do not run gh repo set-default");
  const command = finalReview.locator('[data-command-platform="linux"] code');
  await expect(command).toContainText("gh pr status --repo IDEALLabXP/passport-exercises");
  await expect(command).toContainText(
    "gh pr view practice/ideallabxp --repo IDEALLabXP/passport-exercises --web",
  );
  await expect(command).not.toContainText("{{fork_repository}}");
  await expect(finalReview.locator('[data-command-platform="linux"] [data-copy-command]')).toBeEnabled();
});

test("public manual PR preview cannot copy unresolved repository placeholders", async ({ page }) => {
  await page.goto("/missions/git-manual-pr/");
  const finalReview = page.locator('[data-recipe-step]').filter({ hasText: "Review what GitHub received" });
  const command = finalReview.locator('[data-command-platform="linux"] code');
  await expect(command).toContainText("gh pr status --repo {{fork_repository}}");
  await expect(finalReview.locator('[data-command-platform="linux"] [data-copy-command]')).toBeDisabled();
  await expect(finalReview.getByRole("button", { name: "Available in local Passport" }).last()).toBeDisabled();
});

for (const viewport of [{ width: 1280, height: 900 }, { width: 390, height: 844 }]) {
  test(`manual Git concept support stays bounded at ${viewport.width}px`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await page.addInitScript(() => {
      Object.defineProperty(navigator, "clipboard", { configurable: true, value: {
        writeText: async (text: string) => { sessionStorage.setItem("test-copied-command", text); },
      } });
    });
    await page.goto("/missions/git-manual-pr/");
    const brief = page.locator(".learning-brief");
    await expect(brief.getByRole("heading", { name: "What exactly will your colleague receive?" })).toBeVisible();
    await expect(brief.getByRole("heading", { name: "What will this commit contain?" })).toBeVisible();
    await expect(brief).toContainText("If you commit now, will it include Monday, Room 204, or both? Why?");
    await expect(brief.getByRole("table")).toBeHidden();
    await brief.locator(".learning-explanation summary").focus();
    await page.keyboard.press("Enter");
    await expect(brief.getByRole("table")).toBeVisible();
    await expect(brief).toContainText("meeting-note.md");
    const mission = gitMissionSource();
    for (const question of mission.activity.questions) {
      await expect(brief).not.toContainText(question.prompt);
      for (const option of question.options.filter((item: { id: string }) => question.answer.includes(item.id))) {
        await expect(brief).not.toContainText(option.label);
      }
    }
    const firstStep = page.locator("[data-recipe-step]").first();
    await expect(firstStep).toHaveAttribute("id", "step-prepare");
    await expect(brief.locator("#step-terms")).toHaveCount(1);
    await expect(brief.getByRole("link", { name: "another explanation of staging" })).toHaveAttribute(
      "href", "https://www.w3schools.com/git/git_staging_environment.asp",
    );
    await expect(page.getByRole("button", { name: "Prepare practice folder" })).toBeVisible();
    const writing = page.locator("#step-edit");
    await expect(writing).toContainText("in your own words");
    const model = writing.locator(".recipe-writing-example");
    await expect(model.locator("code")).toBeHidden();
    await model.locator("summary").focus();
    await page.keyboard.press("Enter");
    await expect(model.locator("code")).toBeVisible();
    const expectedModel = mission.activity.recipe.steps.find((step: { id: string }) => step.id === "edit").commands[0].command;
    await model.getByRole("button", { name: "Copy", exact: true }).click();
    await expect.poll(() => page.evaluate(() => sessionStorage.getItem("test-copied-command"))).toBe(expectedModel);
    await model.locator("summary").click();
    await expect(model.locator("code")).toBeHidden();
    for (const id of ["commit", "publish"]) {
      const pause = page.locator(`#step-${id}`);
      await expect(pause.locator(".recipe-instruction")).toContainText(id === "commit" ? "Will your colleague see this new commit on GitHub?" : "Which command shares your commit, and which asks for review?");
      await expect(pause.locator(".recipe-recovery dl")).toBeHidden();
      await pause.locator(".recipe-recovery summary").focus();
      await page.keyboard.press("Enter");
      await expect(pause.locator(".recipe-recovery dl")).toBeVisible();
    }
    const stages = page.locator(".mission-contract a");
    await expect(stages).toHaveCount(3);
    for (const link of await stages.all()) {
      const href = await link.getAttribute("href");
      expect(href).toMatch(/^#[a-z-]+$/);
      await expect(page.locator(href!)).toHaveCount(1);
    }
    await stages.nth(1).click();
    await expect(page).toHaveURL(/#lesson-practice$/);
    const stageStep = page.locator("#step-stage");
    await expect(stageStep.locator(".recipe-observation")).toBeVisible();
    await expect(stageStep.locator(".recipe-recovery dl")).toBeHidden();
    await stageStep.locator(".recipe-recovery summary").click();
    await expect(stageStep.locator(".recipe-recovery dl")).toBeVisible();
    const practiceQuestions = page.locator("[data-learning-question]");
    await expect(practiceQuestions).toHaveCount(["4.0.0", "5.0.0", "6.0.0", "7.0.0", "8.0.0", "9.0.0"].includes(fixtureCurriculum) ? 0 : 2);
    await expect(page.locator(".lesson-reflection")).toHaveCount(0);
    if (!["4.0.0", "5.0.0", "6.0.0", "7.0.0", "8.0.0", "9.0.0"].includes(fixtureCurriculum)) await expect(page.locator("[data-learning-checks]")).toContainText("do not affect your progress");
    if (!["4.0.0", "5.0.0", "6.0.0", "7.0.0", "8.0.0", "9.0.0"].includes(fixtureCurriculum)) await expect(page.getByRole("heading", { name: "Practise", exact: true })).toBeVisible();
    await expect(page.getByRole("heading", { name: ["4.0.0", "5.0.0", "6.0.0", "7.0.0", "8.0.0", "9.0.0"].includes(fixtureCurriculum) ? "Check and submit" : "Questions to submit", exact: true })).toBeVisible();
    await expect(page.locator(".lesson-threshold")).toContainText(["4.0.0", "5.0.0", "6.0.0", "7.0.0", "8.0.0", "9.0.0"].includes(fixtureCurriculum) ? "Every answer must be correct." : "Both answers must be correct.");
    await expect(page.locator("[data-activity-result]")).toContainText("You can try these questions.");
    await expect(page.locator("[data-preview-explanation]")).toBeVisible();
    await expect(page.locator("[data-local-explanation]")).toBeHidden();
    for (const question of await practiceQuestions.all()) {
      const feedback = question.locator("[data-learning-feedback]");
      await expect(feedback).toBeHidden();
      await checkVisible(question.locator('input[value="wrong_a"]'));
      await expect(feedback).toHaveAttribute("data-correct", "false");
      await expect(feedback).toContainText("Try again.");
      const id = await question.getAttribute("data-learning-question");
      const sourceQuestion = mission.activity.learning_checks.find((item: { id: string }) => item.id === id);
      await expect(feedback).toContainText(sourceQuestion.hint);
      await expect(feedback).not.toContainText(sourceQuestion.feedback);
      const explanation = question.locator("[data-learning-explanation]");
      await expect(explanation.locator("p")).toBeHidden();
      await explanation.locator("summary").focus();
      await page.keyboard.press("Enter");
      await expect(explanation.locator("p")).toHaveText(sourceQuestion.feedback);
      await explanation.locator("summary").click();
      await checkVisible(question.locator('input[value="wrong_b"]'));
      await expect(feedback).toHaveAttribute("data-correct", "false");
      await question.locator('input[value="correct"]').focus();
      await page.keyboard.press("Space");
      await expect(feedback).toHaveAttribute("data-correct", "true");
      await expect(feedback).toContainText("That's right.");
    }
    if (!["4.0.0", "5.0.0", "6.0.0", "7.0.0", "8.0.0", "9.0.0"].includes(fixtureCurriculum)) {
      await expect(page.locator('[data-learning-question="saved_after_staging"] [data-learning-feedback]')).toContainText("Stage the note again to select the newer saved version");
      await expect(page.locator('[data-learning-question="local_commit_visibility"] [data-learning-feedback]')).toContainText("Neither action merges it into main");
    }
    await expect(page.locator("[data-activity-form] [data-question]")).toHaveCount(["4.0.0", "5.0.0", "6.0.0", "7.0.0", "8.0.0", "9.0.0"].includes(fixtureCurriculum) ? 3 : 2);
    await expect(page.getByRole("button", { name: "Submit lesson" })).toBeDisabled();
    await page.locator(".recipe-command-guide summary").click();
    await expect(page.locator(".recipe-command-guide-content")).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await assertAccessible(page);
  });
}

test("manual Git prediction, learning checks and help work without JavaScript", async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false, reducedMotion: "reduce" });
  const page = await context.newPage();
  await page.goto("/missions/git-manual-pr/");
  const explanation = page.locator(".learning-brief .learning-explanation");
  await expect(explanation.getByRole("table")).toBeHidden();
  await explanation.locator("summary").focus();
  await page.keyboard.press("Enter");
  await expect(explanation.getByRole("table")).toBeVisible();
  for (const question of await page.locator("[data-learning-question]").all()) {
    const explanation = question.locator("[data-learning-explanation]");
    await expect(explanation.locator("p")).toBeHidden();
    await explanation.locator("summary").click();
    await expect(explanation.locator("p")).toBeVisible();
  }
  const model = page.locator("#step-edit .recipe-writing-example");
  await expect(model.locator("code")).toBeHidden();
  await model.locator("summary").focus();
  await page.keyboard.press("Enter");
  await expect(model.locator("code")).toBeVisible();
  const recovery = page.locator("#step-stage .recipe-recovery");
  await recovery.locator("summary").click();
  await expect(recovery.locator("dl")).toBeVisible();
  const guide = page.locator(".recipe-command-guide");
  await guide.locator("summary").click();
  await expect(guide).toHaveAttribute("open", "");
  for (const platform of ["windows", "macos", "linux"]) {
    await expect(guide.locator(`[data-command-guide-platform="${platform}"]`)).toBeVisible();
  }
  await expect(guide.getByRole("link", { name: "How to open a terminal and run a command" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Submit lesson" })).toBeDisabled();
  await context.close();
});

test("manual Git clarification preserves an existing draft and the check-before-submit gate", async ({ page }) => {
  const base = localContext();
  const current = ["4.0.0", "5.0.0", "6.0.0", "7.0.0", "8.0.0", "9.0.0"].includes(fixtureCurriculum);
  const correct = current ? { commit_snapshot: ["staged"], share_commit: ["published"], review_correction: ["amend"] } : { pr_roles: ["correct"], staged_diff: ["correct"] };
  const draft = current ? { ...correct, share_commit: ["latest"] } : { pr_roles: ["correct"], staged_diff: ["wrong_a"] };
  let context = localContext({
    repository: { ...base.repository, passport: { ...base.repository.passport, missions: ["git-tools-auth", "git-manual-pr"] } },
    official: { status: { current_mission: "git-manual-pr", head_sha: "b".repeat(40), stage: "in_progress" } },
    local_state: {
      ...base.local_state,
      last_opened_mission: "git-manual-pr",
      mission_drafts: { "git-manual-pr": draft },
      attempt_counts: { "git-manual-pr": 2 },
    },
  });
  const routeBefore = context.repository.passport.missions;
  let verifyCalls = 0;
  let submitCalls = 0;
  await page.route("**/__passport/v1/context", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify(context) }));
  await page.route("**/__passport/v1/state", async (route) => {
    context = { ...context, local_state: route.request().postDataJSON() };
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ status: "saved_locally_not_completion" }) });
  });
  await page.route("**/__passport/v2/verify", async (route) => {
    verifyCalls += 1;
    expect(route.request().postDataJSON().answers).toEqual(correct);
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ status: "ready_to_submit", score_percent: 100, critical_passed: true, feedback: [] }) });
  });
  await page.route("**/__passport/v2/submit", async (route) => {
    submitCalls += 1;
    expect(route.request().postDataJSON().answers).toEqual(correct);
    await route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ status: "queued", mission: "git-manual-pr" }) });
  });
  await page.route("**/__passport/v1/sync", (route) => route.fulfill({ status: 503, contentType: "application/json", body: "{}" }));
  await page.goto("/missions/git-manual-pr/");
  const roles = page.locator(current ? '[data-question="commit_snapshot"] input[value="staged"]' : '[data-question="pr_roles"] input[value="correct"]');
  const staged = page.locator(current ? '[data-question="share_commit"] input[value="latest"]' : '[data-question="staged_diff"] input[value="wrong_a"]');
  await expect(roles).toBeChecked();
  await expect(staged).toBeChecked();
  await expect(page.locator("[data-preview-explanation]")).toBeHidden();
  await expect(page.locator("[data-local-explanation]")).toBeVisible();
  await page.locator(".recipe-command-guide summary").click();
  await page.locator(".learning-brief .learning-explanation summary").click();
  const draftsBeforePractice = structuredClone(context.local_state.mission_drafts);
  if (!current) await checkVisible(page.locator('[data-learning-question="saved_after_staging"] input[value="wrong_a"]'));
  if (!current) await checkVisible(page.locator('[data-learning-question="local_commit_visibility"] input[value="correct"]'));
  expect(context.local_state.mission_drafts).toEqual(draftsBeforePractice);
  await page.locator("#step-stage .recipe-recovery summary").click();
  await page.locator("#step-edit .recipe-writing-example summary").click();
  await page.locator("#step-commit .recipe-recovery summary").click();
  await page.locator("#step-publish .recipe-recovery summary").click();
  await page.reload();
  await expect(roles).toBeChecked();
  await expect(staged).toBeChecked();
  expect(context.repository.passport.missions).toEqual(routeBefore);
  expect(context.local_state.attempt_counts).toEqual({ "git-manual-pr": 2 });
  expect(verifyCalls).toBe(0);
  expect(submitCalls).toBe(0);
  if (!current) await expect(page.locator('[data-learning-question="saved_after_staging"] input:checked')).toHaveCount(0);
  if (!current) await expect(page.locator("[data-learning-feedback]").first()).toBeHidden();
  if (!current) await checkVisible(page.locator('[data-learning-question="saved_after_staging"] input[value="wrong_a"]'));
  await checkVisible(page.locator(current ? '[data-question="share_commit"] input[value="published"]' : '[data-question="staged_diff"] input[value="correct"]'));
  await checkVisible(page.getByLabel(/I reviewed the answers/));
  await checkVisible(page.getByLabel(/This public submission contains no credentials/));
  await checkVisible(page.getByLabel(/I personally observed every result/));
  const submit = page.getByRole("button", { name: "Submit lesson" });
  await expect(submit).toBeDisabled();
  await page.getByRole("button", { name: "Check my work" }).click();
  await expect(submit).toBeEnabled();
  await submit.click();
  await expect(page.getByText("Submitted to GitHub", { exact: true })).toBeVisible();
  expect(verifyCalls).toBe(1);
  expect(submitCalls).toBe(1);
});

test("practice-folder errors appear beside the retry button", async ({ page }) => {
  const base = localContext();
  const context = localContext({
    repository: {
      ...base.repository,
      passport: { ...base.repository.passport, missions: ["git-tools-auth"] },
    },
    local_state: { ...base.local_state, last_opened_mission: "git-tools-auth" },
  });
  await page.route("**/__passport/v1/context", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify(context),
  }));
  await page.route("**/__passport/v2/practice", async (route) => route.fulfill({
    status: 409,
    contentType: "application/json",
    body: JSON.stringify({ error: "practice_origin_mismatch" }),
  }));
  await page.goto("/missions/git-tools-auth/");
  await page.getByRole("button", { name: "Prepare practice folder" }).click();
  await expect(page.locator("[data-practice-status]")).toHaveText("The existing practice folder belongs to another repository. Nothing was changed. Run gh passport doctor.");
  await expect(page.getByRole("button", { name: "Try preparing again" })).toBeEnabled();
});

test("command references remain understandable when opened directly", async ({ page }) => {
  await page.goto("/reference/euler/slurm/");
  await expect(page.locator(".reference-command-primer")).toHaveCount(0);
  await expect(page.locator(".reference-markdown")).toContainText("Euler login node - Bash");
  await expect(page.locator(".reference-markdown").getByRole("link", { name: "how to open a terminal and run one command" })).toBeVisible();
  await page.getByText("Find a section on this page", { exact: true }).click();
  await expect(page.getByRole("navigation", { name: "Sections on this page" }).getByRole("link", { name: "Completed-Job Accounting" })).toHaveAttribute("href", "#completed-job-accounting");
});

test("copy controls expose commands without prompts or explanatory comments", async ({ page }) => {
  await page.goto("/reference/euler/slurm/");
  const button = page.getByRole("button", { name: /Copy bash commands/ }).first();
  await expect(button).toBeVisible();
  const copied = await button.getAttribute("data-copy-value");
  expect(copied).toBeTruthy();
  expect(copied).not.toMatch(/^\s*\$\s/m);
  expect(copied).not.toMatch(/^\s*#(?:\s|$)/m);
});

test("keyboard search finds symptom-first SSH help", async ({ page }) => {
  await page.goto("/");
  await page.keyboard.press("/");
  const search = page.getByRole("dialog", { name: "Find a lesson or fix" });
  await expect(search).toBeVisible();
  await search.getByRole("searchbox").fill("SSH permission");
  await expect(search.getByRole("link", { name: /Euler Access And Safe SSH Key Setup/ })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(search).toBeHidden();
});

test("static dashboard is an honest preview with no synthetic completion state", async ({ page }) => {
  await page.goto("/passport/");
  await expect(page.locator("[data-runtime-label]")).toHaveText("Public course preview");
  await expect(page.getByRole("heading", { name: "Open your Passport", exact: true })).toBeVisible();
  await expect(page.locator("[data-sync-panel]")).toBeHidden();
  await expect(page.getByText("Controller verified", { exact: true })).toHaveCount(0);
  await expect(page.getByText("Needs work", { exact: true })).toHaveCount(0);
  await assertAccessible(page);
});

test("local dashboard labels navigation as non-authoritative", async ({ page }) => {
  await page.route("**/__passport/v1/context", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify(localContext()),
  }));
  await page.route("**/__passport/v1/sync", async (route) => route.fulfill({ status: 503, body: "{}" }));
  await page.goto("/passport/");
  await expect(page.getByText("Your local passport", { exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "Resume my passport" })).toBeVisible();
  await expect(page.getByText("Last page you visited", { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: /Protect Accounts/ })).toBeVisible();
  await expect(page.getByText("Local only", { exact: true })).toBeVisible();
  await expect(page.getByText("Latest saved version has not reached GitHub")).toBeVisible();
  await expect(page.getByText("GitHub submission details")).toBeHidden();
  await expect(page.getByText("Technical details", { exact: true })).toBeHidden();
  await expect(page.getByText("Controller verified", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("link", { name: /Request help without posting secrets/ })).toHaveAttribute(
    "href",
    "https://github.com/soheylm-passport-sandbox/passport-exercises/issues/new?template=passport-help.yml",
  );
});

test("local dashboard offers only the launcher-selected compatible update", async ({ page }) => {
  let updatePosts = 0;
  await page.route("**/__passport/v1/context", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify(localContext()),
  }));
  await page.route("**/__passport/v1/sync", async (route) => route.fulfill({ status: 503, body: "{}" }));
  await page.route("**/__passport/v1/update", async (route) => {
    if (route.request().method() === "POST") {
      updatePosts += 1;
      expect(route.request().postData()).toBeNull();
      await route.fulfill({
        status: 202,
        contentType: "application/json",
        body: JSON.stringify({ status: "update_starting", version: "v0.4.3" }),
      });
      return;
    }
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        state: "available",
        current_version: "v0.4.2",
        curriculum_version: fixtureCurriculum,
        available: { version: "v0.4.3", size_bytes: 12_345 },
      }),
    });
  });
  await page.goto("/passport/");
  await expect(page.getByText("Passport v0.4.3 is ready")).toBeVisible();
  await expect(page.getByText(/saved place, answers, and submitted work will be kept/)).toBeVisible();
  await page.getByRole("button", { name: "Update and reopen" }).click();
  await expect(page.getByText(/will reopen in a new tab/)).toBeVisible();
  expect(updatePosts).toBe(1);
});

test("local dashboard ignores a rollback recorded by an older launcher", async ({ page }) => {
  await page.route("**/__passport/v1/context", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify(localContext()),
  }));
  await page.route("**/__passport/v1/sync", async (route) => route.fulfill({ status: 503, body: "{}" }));
  await page.route("**/__passport/v1/update", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify({
      state: "up_to_date",
      current_version: "v0.5.2",
      curriculum_version: fixtureCurriculum,
      last_result: {
        status: "rolled_back",
        version: "v0.5.0",
        recorded_at: "2026-09-06T10:00:00Z",
        message: "The previous launcher was restored.",
      },
    }),
  }));
  await page.goto("/passport/");
  await expect(page.getByText("The previous Passport was restored")).toBeHidden();
  await expect(page.locator("[data-update-banner]")).toBeHidden();
});

test("public preview never exposes a launcher update control", async ({ page }) => {
  await page.goto("/passport/");
  await expect(page.getByRole("button", { name: "Update and reopen" })).toBeHidden();
});

test("submitted work waits without inventing progress or requesting another push", async ({ page }) => {
  const syncResult = {
    state: "waiting_for_controller",
    pull_request_number: 7,
    pull_request_url: "https://github.com/soheylm-passport-sandbox/passport-exercises/pull/7",
    repository_url: "https://github.com/student/passport-exercises",
    recovery_issue_url: "https://github.com/soheylm-passport-sandbox/passport-exercises/issues/new?template=passport-help.yml",
    remote_head_sha: "b".repeat(40),
    synced_at: "2026-08-30T12:00:00Z",
  };
  const context = localContext({
    local_state: {
      ...localContext().local_state,
      pending_submissions: {
        "core-orientation": {
          head_sha: "b".repeat(40),
          submitted_at: "2026-08-30T12:00:00Z",
        },
      },
    },
    sync_result: syncResult,
  });
  await page.route("**/__passport/v1/context", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify(context),
  }));
  await page.route("**/__passport/v1/sync", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify(syncResult),
  }));
  await page.goto("/passport/");
  await expect(page.getByText("Submitted to GitHub; waiting for the automatic check")).toBeVisible();
  await expect(page.getByText(/Do not submit the same change again/)).toBeVisible();
  await expect(page.locator("[data-first-check-note]")).toBeVisible();
  await expect(page.locator("[data-first-check-note]")).toContainText("may be waiting for a maintainer to activate your enrollment");
  await expect(page.locator("[data-first-check-note]")).toContainText("This page cannot confirm your enrollment status");
  await expect(page.getByText("Submitted; checking", { exact: true })).toHaveCount(2);
  await expect(page.getByText("Not checked", { exact: true })).toHaveCount(1);
  await expect(page.getByText("Submitted lesson", { exact: true })).toBeVisible();
  await expect(page.getByText("Waiting for the automatic GitHub check", { exact: true })).toBeVisible();
  await expect(page.getByText("GitHub submission details")).toBeHidden();
  await expect(page.getByText("Technical details", { exact: true })).toBeHidden();
  // A missing check on a later submission must not suggest a new enrollment problem.
  await page.route("**/__passport/v1/context", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify({ ...context, local_state: {
      ...context.local_state,
      pending_submissions: { "core-accounts-secrets": {
        head_sha: "b".repeat(40), submitted_at: "2026-08-30T12:00:00Z",
      } },
    } }),
  }));
  await page.reload();
  await expect(page.locator("[data-waiting-banner]")).toBeVisible();
  await expect(page.locator("[data-first-check-note]")).toBeHidden();
});

test("dashboard picks up the trusted result without a manual refresh", async ({ page, browserName }) => {
  test.skip(browserName !== "chromium", "automatic polling timing is covered once");
  await page.clock.install();
  const head = "b".repeat(40);
  const waiting = {
    state: "waiting_for_controller",
    pull_request_number: 7,
    pull_request_url: "https://github.com/soheylm-passport-sandbox/passport-exercises/pull/7",
    repository_url: "https://github.com/student/passport-exercises",
    recovery_issue_url: "https://github.com/soheylm-passport-sandbox/passport-exercises/issues/new?template=passport-help.yml",
    remote_head_sha: head,
    synced_at: "2026-09-05T12:00:00Z",
  };
  const official = {
    source: "github_controller_check",
    controller_app_id: 42,
    check_run_id: 99,
    check_run_url: "https://github.com/soheylm-passport-sandbox/passport-exercises/runs/99",
    check_conclusion: "success",
    pull_request_number: 7,
    pull_request_url: waiting.pull_request_url,
    repository_url: waiting.repository_url,
    recovery_issue_url: waiting.recovery_issue_url,
    remote_head_sha: head,
    synced_at: "2026-09-05T12:00:10Z",
    status: {
      schema_version: 2,
      curriculum_version: fixtureCurriculum,
      head_sha: head,
      current_mission: "core-accounts-secrets",
      missions: [
        { id: "core-orientation", status: "passed" },
        { id: "core-accounts-secrets", status: "needs_work" },
      ],
      stage: "working",
      review_state: "not_required",
      assessed_at: "2026-09-05T12:00:10Z",
    },
  };
  const context = localContext({
    local_state: {
      ...localContext().local_state,
      pending_submissions: {
        "core-orientation": { head_sha: head, submitted_at: "2026-09-05T12:00:00Z" },
      },
    },
  });
  let syncCalls = 0;
  await page.route("**/__passport/v1/context", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify(context),
  }));
  await page.route("**/__passport/v1/sync", async (route) => {
    syncCalls += 1;
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify(syncCalls === 1 ? waiting : { ...waiting, state: "verified", official, synced_at: official.synced_at }),
    });
  });
  await page.goto("/passport/");
  await expect(page.getByText("Submitted; checking", { exact: true })).toHaveCount(2);
  await expect(page.locator("[data-first-check-note]")).toBeVisible();
  await page.clock.fastForward(10_100);
  await expect.poll(() => syncCalls).toBeGreaterThanOrEqual(2);
  await expect(page.locator("[data-current-status]")).toHaveText("Needs work");
  await expect(page.getByText("Submitted; checking", { exact: true })).toHaveCount(0);
  await expect(page.getByText("Automatic check loaded", { exact: true })).toBeVisible();
  await expect(page.locator("[data-first-check-note]")).toBeHidden();
});

test("missing public record provides one safe recovery path", async ({ page }) => {
  const syncResult = {
    state: "assessment_recovery_required",
    repository_url: "https://github.com/student/passport-exercises",
    recovery_issue_url: "https://github.com/soheylm-passport-sandbox/passport-exercises/issues/new?template=passport-help.yml",
    synced_at: "2026-08-30T12:00:00Z",
  };
  await page.route("**/__passport/v1/context", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify(localContext({ sync_result: syncResult })),
  }));
  await page.route("**/__passport/v1/sync", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify(syncResult),
  }));
  await page.goto("/passport/");
  await expect(page.getByText("The public learning record needs safe recovery")).toBeVisible();
  await expect(page.getByText(/run gh passport start/)).toBeVisible();
  await expect(page.getByRole("link", { name: /Request help if automatic recovery stops/ })).toHaveAttribute(
    "href",
    "https://github.com/soheylm-passport-sandbox/passport-exercises/issues/new?template=passport-help.yml",
  );
});

test("a local mission requires a check before one submission", async ({ page }) => {
  let verifyCalls = 0;
  let submitCalls = 0;
  let syncCalls = 0;
  const context = localContext({
    repository: {
      ...localContext().repository,
      passport: {
        ...localContext().repository.passport,
        missions: ["core-orientation"],
      },
    },
    local_state: {
      ...localContext().local_state,
      last_opened_mission: "core-orientation",
    },
  });
  await page.route("**/__passport/v1/context", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify(context),
  }));
  await page.route("**/__passport/v1/state", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify({ status: "saved_locally_not_completion" }),
  }));
  await page.route("**/__passport/v2/verify", async (route) => {
    verifyCalls += 1;
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ status: "ready_to_submit", score_percent: 100, critical_passed: true, feedback: [] }),
    });
  });
  await page.route("**/__passport/v2/submit", async (route) => {
    submitCalls += 1;
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({ status: "queued", mission: "core-orientation" }),
    });
  });
  await page.route("**/__passport/v1/sync", async (route) => {
    syncCalls += 1;
    await route.fulfill({ status: 503, contentType: "application/json", body: "{}" });
  });
  await page.goto("/missions/core-orientation/");
  const submit = page.getByRole("button", { name: "Submit lesson" });
  await expect(submit).toBeDisabled();
  await page.getByLabel("The automatic Passport check shown on GitHub for the submitted work.").check();
  await page.getByLabel("Run gh passport open; the launcher remembers the managed folder on this computer.").check();
  await page.getByLabel(/I reviewed the answers/).check();
  await page.getByLabel(/This public submission contains no credentials/).check();
  await page.getByLabel(/I personally observed every result/).check();
  await expect(submit).toBeDisabled();
  await page.getByRole("button", { name: "Check my work" }).click();
  await expect(page.getByText("Ready to submit", { exact: true })).toBeVisible();
  await expect(submit).toBeEnabled();
  await submit.click();
  await expect(page.getByText("Submitted to GitHub", { exact: true })).toBeVisible();
  await expect.poll(() => syncCalls).toBe(1);
  await page.waitForTimeout(250);
  expect(syncCalls).toBe(1);
  expect(verifyCalls).toBe(1);
  expect(submitCalls).toBe(1);
});

test("a failed knowledge check clears answers and changes presentation", async ({ page }) => {
  let context = localContext({
    repository: {
      ...localContext().repository,
      passport: {
        ...localContext().repository.passport,
        missions: ["core-orientation"],
      },
    },
    local_state: {
      ...localContext().local_state,
      last_opened_mission: "core-orientation",
    },
  });
  await page.route("**/__passport/v1/context", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify(context),
  }));
  let savedAttempts = 0;
  await page.route("**/__passport/v1/state", async (route) => {
    const localState = route.request().postDataJSON();
    savedAttempts = localState.attempt_counts?.["core-orientation"] || 0;
    context = { ...context, local_state: localState };
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ status: "saved_locally_not_completion" }),
    });
  });
  await page.route("**/__passport/v2/verify", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify({
      status: "needs_work",
      score_percent: 50,
      critical_passed: false,
      next_variant: 1,
      feedback: [{ question: "status_source", correct: false, message: "Use the automatic Passport check shown on GitHub." }],
    }),
  }));
  await page.goto("/missions/core-orientation/");
  const questions = page.locator("[data-question]");
  const initialOrder = await questions.evaluateAll((items) => items.map((item) => item.getAttribute("data-question")));
  await page.getByLabel("The page stored in local browser history.").check();
  await page.getByLabel("Run gh passport open; the launcher remembers the managed folder on this computer.").check();
  await page.getByRole("button", { name: "Check my work" }).click();
  await expect(page.getByText(/same skills are now presented in a different order/)).toBeVisible();
  await expect(page.getByLabel("The page stored in local browser history.")).not.toBeChecked();
  const retryOrder = await questions.evaluateAll((items) => items.map((item) => item.getAttribute("data-question")));
  expect(retryOrder).not.toEqual(initialOrder);
  await expect.poll(() => savedAttempts).toBe(1);
  await page.reload();
  const resumedOrder = await questions.evaluateAll((items) => items.map((item) => item.getAttribute("data-question")));
  expect(resumedOrder).toEqual(retryOrder);
  await expect(page.getByRole("button", { name: "Submit lesson" })).toBeDisabled();
});

test("correct answers stay selected when a computer check still needs work", async ({ page }) => {
  const base = localContext();
  const context = localContext({
    repository: {
      ...base.repository,
      passport: { ...base.repository.passport, missions: ["git-tools-auth"] },
    },
    local_state: {
      ...base.local_state,
      last_opened_mission: "git-tools-auth",
    },
  });
  await page.route("**/__passport/v1/context", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify(context),
  }));
  await page.route("**/__passport/v1/state", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify({ status: "saved_locally_not_completion" }),
  }));
  await page.route("**/__passport/v2/verify", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify({
      status: "needs_work",
      score_percent: 100,
      critical_passed: true,
      next_variant: 1,
      feedback: [
        { question: "auth_scope", correct: true, message: "Correct." },
        { question: "safe_diagnosis", correct: true, message: "Correct." },
      ],
      receipt: {
        verifier: "git_environment",
        passed: false,
        checks: {
          git: true,
          github_auth: true,
          identity_name: false,
          identity_email: true,
          remote_credentials_absent: true,
          practice_repository: true,
          practice_branch: true,
        },
      },
    }),
  }));

  await page.goto("/missions/git-tools-auth/");
  const accountAnswer = page.getByLabel("The intended GitHub account is authenticated.");
  const diagnosisAnswer = page.getByLabel("Read the named check and follow the recovery for that check.");
  await accountAnswer.check();
  await diagnosisAnswer.check();
  await page.getByRole("button", { name: "Check my work" }).click();

  await expect(page.getByText("Finish the checks on this computer", { exact: true })).toBeVisible();
  await expect(page.locator("[data-activity-result]")).toHaveAttribute("data-kind", "action-required");
  await expect(page.getByText(/100% answer score\. Your answers are correct/)).toBeVisible();
  await expect(page.getByText("6 of 7 computer checks passed", { exact: true })).toBeVisible();
  await expect(page.getByText("Fix: A commit author name is configured", { exact: true })).toBeVisible();
  await expect(accountAnswer).toBeChecked();
  await expect(diagnosisAnswer).toBeChecked();
  await expect(page.getByRole("button", { name: "Submit lesson" })).toBeDisabled();
});

test("Euler account mismatch explains the full sacct value without resubmission", async ({ page }) => {
  const base = localContext();
  await page.route("**/__passport/v1/context", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify(localContext({
      repository: { ...base.repository, passport: { ...base.repository.passport, missions: ["euler-cpu-first-job"] } },
      local_state: { ...base.local_state, last_opened_mission: "euler-cpu-first-job" },
    })),
  }));
  await page.route("**/__passport/v1/state", async (route) => route.fulfill({
    contentType: "application/json", body: JSON.stringify({ status: "saved_locally_not_completion" }),
  }));
  await page.route("**/__passport/v2/verify", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify({
      status: "needs_work", score_percent: 100, critical_passed: true, next_variant: 1,
      feedback: [{ question: "login_node", correct: true, message: "Correct." }, { question: "empty_queue", correct: true, message: "Correct." }],
      receipt: { verifier: "euler_job", passed: false, checks: { correct_account: false, zero_exit: true } },
    }),
  }));

  await page.goto("/missions/euler-cpu-first-job/");
  await expect(page.getByLabel("Account shown by sacct")).toHaveAttribute("placeholder", "normal/es_fuge");
  await expect(page.getByText("Account%40 gives the account column room")).toBeVisible();
  await page.getByLabel("On a Slurm-allocated compute node.").check();
  await page.getByLabel("Use sacct and inspect the job logs; it may already have finished.").check();
  await page.getByRole("button", { name: "Check my work" }).click();
  await expect(page.getByText("Fix: Euler job account", { exact: true })).toBeVisible();
  await expect(page.getByText(/enter the full Account value from sacct: normal\/es_fuge/)).toBeVisible();
  await expect(page.getByText(/Do not resubmit the job\./)).toBeVisible();
});

for (const width of [1280,390]) {
  test(`Python feedback names the actual repair and preserves answers at ${width}px`, async ({page}) => {
    await page.setViewportSize({width,height:844});
    const errors: string[]=[];page.on("pageerror",error=>errors.push(error.message));
    const base=localContext();
    const context=localContext({repository:{...base.repository,passport:{...base.repository.passport,curriculum_version:"3.0.0",missions:["python-reproducible-change"]}},local_state:{...base.local_state,curriculum_version:"3.0.0",last_opened_mission:"python-reproducible-change"}});
    await page.route("**/__passport/v1/context",route=>route.fulfill({json:context}));
    await page.route("**/__passport/v1/state",route=>route.fulfill({json:{status:"saved_locally_not_completion"}}));
    let repaired=false;
    await page.route("**/__passport/v2/verify",route=>route.fulfill({json:{status:repaired?"ready_to_submit":"needs_work",score_percent:100,critical_passed:true,next_variant:1,feedback:[],receipt:{verifier:"python_project_v2",passed:repaired,checks:{semantic_model:repaired,bounded_diff:true},...(repaired?{}:{feedback:"Calculate expected values independently; do not obtain them by calling the function being tested."})}}}));
    await page.goto("/missions/python-reproducible-change/");
    const choices=page.locator('[data-question] input[value="correct"]');
    await expect(choices).toHaveCount(2);for(const input of await choices.all())await input.check();
    await page.getByRole("button",{name:"Check my work"}).click();
    await expect(page.locator("[data-activity-result]")).toContainText("Calculate expected values independently");
    await expect(page.getByText("Fix: Python calculation and discovered tests",{exact:true})).toBeVisible();
    await expect(page.getByRole("button",{name:"Submit lesson"})).toBeDisabled();
    for(const input of await choices.all())await expect(input).toBeChecked();
    repaired=true;await page.getByRole("button",{name:"Check my work"}).click();
    await expect(page.getByText("Ready to submit",{exact:true})).toBeVisible();
    await expect(page.getByRole("button",{name:"Submit lesson"})).toBeEnabled();
    for(const input of await choices.all())await expect(input).toBeChecked();
    expect(errors).toEqual([]);await assertAccessible(page);
  });
}

test("agent policy failures show the controller guidance and preserve answers", async ({ page }) => {
  const base = localContext();
  await page.route("**/__passport/v1/context", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify(localContext({
      repository: { ...base.repository, passport: { ...base.repository.passport, missions: ["ai-agent-baseline"] } },
      local_state: { ...base.local_state, last_opened_mission: "ai-agent-baseline" },
    })),
  }));
  await page.route("**/__passport/v1/state", async (route) => route.fulfill({
    contentType: "application/json", body: JSON.stringify({ status: "saved_locally_not_completion" }),
  }));
  await page.route("**/__passport/v2/verify", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify({
      status: "needs_work", score_percent: 100, critical_passed: true, next_variant: 1,
      feedback: [],
      receipt: {
        verifier: "ai_project", passed: false,
        checks: { bounded_file: true, bounded_diff: true, canary_unchanged: true, durable_p: false, temporary_d: false, avoid_c: false, heavy_compute: false },
      },
    }),
  }));
  await page.goto("/missions/ai-agent-baseline/");
  const selections = ["7.0.0", "8.0.0", "9.0.0"].includes(fixtureCurriculum)
    ? [["file_instruction", "keep_scope"], ["approve_action", "reject_command"], ["verify_scope", "inspect_extra"]]
    : [["agent_scope", "correct"], ["agent_result", "correct"]];
  const choices = selections.map(([id,value]) => page.locator(`[data-question="${id}"] input[value="${value}"]`));
  for (const choice of choices) await choice.check();
  await page.getByRole("button", { name: "Check my work" }).click();
  for (const rule of agentContract.rules) {
    await expect(page.getByText(`Fix: ${rule.label}`, { exact: true })).toBeVisible();
    await expect(page.getByText(rule.message, { exact: true })).toBeVisible();
  }
  for (const choice of choices) await expect(choice).toBeChecked();
  await expect(page.getByRole("button", { name: "Submit lesson" })).toBeDisabled();
});

test("answer order is stable for one Passport and differs between Passports", async ({ page }) => {
  let passportID = "passport-alpha";
  const missionContext = () => {
    const base = localContext();
    return localContext({
      repository: {
        ...base.repository,
        passport: { ...base.repository.passport, missions: ["core-orientation"] },
      },
      local_state: {
        ...base.local_state,
        passport_id: passportID,
        last_opened_mission: "core-orientation",
      },
    });
  };
  await page.route("**/__passport/v1/context", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify(missionContext()),
  }));
  const answerOrder = () => page.locator("[data-question]").evaluateAll((questions) =>
    Object.fromEntries(questions.map((question) => [
      question.getAttribute("data-question"),
      Array.from(question.querySelectorAll(".answer-options input")).map((input) => input.getAttribute("value")),
    ])),
  );

  await page.goto("/missions/core-orientation/");
  const alpha = await answerOrder();
  await page.reload();
  expect(await answerOrder()).toEqual(alpha);

  passportID = "passport-beta";
  await page.reload();
  expect(await answerOrder()).not.toEqual(alpha);
});

test("future lessons remain readable but cannot be submitted early", async ({ page }) => {
  await page.route("**/__passport/v1/context", async (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify(localContext({
      local_state: {
        ...localContext().local_state,
        last_opened_mission: "core-orientation",
      },
    })),
  }));
  await page.goto("/missions/core-accounts-secrets/");
  await expect(page.getByRole("heading", { level: 1 })).toContainText("Protect Accounts And Secrets");
  await expect(page.getByText("Lesson locked", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Check my work" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Submit lesson" })).toBeDisabled();
});

test("mobile layout exposes navigation and route action without horizontal overflow", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await expect(page.getByRole("link", { name: "How to get your passport" })).toBeVisible();
  await page.getByText("Menu", { exact: true }).click();
  await expect(page.getByRole("navigation", { name: "Compact navigation" })).toBeVisible();
  const layout = await page.evaluate(() => ({
    overflow: document.documentElement.scrollWidth - window.innerWidth,
    offenders: Array.from(document.querySelectorAll("body *"))
      .map((element) => ({
        selector: `${element.tagName.toLowerCase()}.${Array.from(element.classList).join(".")}`,
        left: Math.round(element.getBoundingClientRect().left),
        right: Math.round(element.getBoundingClientRect().right),
      }))
      .filter((item) => item.left < -1 || item.right > window.innerWidth + 1)
      .slice(0, 10),
  }));
  expect(layout.overflow, JSON.stringify(layout.offenders)).toBeLessThanOrEqual(1);
  await assertAccessible(page);
});

test("representative desktop and mobile visual baselines render", async ({ page, browserName }, testInfo) => {
  const views = [
    { name: "desktop-mission", route: "/missions/core-orientation/", width: 1440, height: 1000 },
    { name: "mobile-getting-started", route: "/getting-started/", width: 390, height: 844 },
    { name: "mobile-start", route: "/start/", width: 390, height: 844 },
  ];
  for (const view of views) {
    await page.setViewportSize({ width: view.width, height: view.height });
    await page.goto(view.route);
    const screenshot = await page.screenshot({ animations: "disabled", fullPage: false });
    expect(screenshot.byteLength, `${browserName} ${view.name} screenshot is unexpectedly empty`).toBeGreaterThan(5000);
    await testInfo.attach(`${browserName}-${view.name}`, { body: screenshot, contentType: "image/png" });
  }
});

test("all generated pages have one H1, unique ids, and no document overflow", async ({ page, browserName }) => {
  test.setTimeout(120_000);
  test.skip(browserName !== "chromium", "full generated-page invariant runs once");
  const search = await (await page.request.get("/data/search.json")).json();
  const routes = new Set<string>(["/", "/getting-started/", "/start/", "/passport/", "/404.html"]);
  for (const item of search) routes.add(item.route);
  for (const track of ["core", "git", "python", "ai-agents", "euler-cpu", "euler-gpu", "data-steward", "blade", "supervisor"]) {
    routes.add(`/tracks/${track}/`);
  }
  for (const width of [320, 390, 768, 1024]) {
    await page.setViewportSize({ width, height: 900 });
    for (const route of routes) {
      await page.goto(route);
      const result = await page.evaluate(() => {
        const ids = Array.from(document.querySelectorAll("[id]")).map((node) => node.id);
        return {
          overflow: document.documentElement.scrollWidth - window.innerWidth,
          h1: document.querySelectorAll("h1").length,
          duplicateIds: ids.filter((id, index) => ids.indexOf(id) !== index),
          offenders: Array.from(document.querySelectorAll("body *"))
            .map((element) => ({
              selector: `${element.tagName.toLowerCase()}.${Array.from(element.classList).join(".")}`,
              text: (element.textContent || "").trim().slice(0, 80),
              left: Math.round(element.getBoundingClientRect().left),
              right: Math.round(element.getBoundingClientRect().right),
            }))
            .filter((item) => item.left < -1 || item.right > window.innerWidth + 1)
            .slice(0, 10),
        };
      });
      expect(result.h1, `${route} at ${width}px`).toBe(1);
      expect(result.duplicateIds, `${route} at ${width}px`).toEqual([]);
      expect(result.overflow, `${route} at ${width}px: ${JSON.stringify(result.offenders)}`).toBeLessThanOrEqual(1);
    }
  }
});

test("unknown routes provide a useful recovery path", async ({ page }) => {
  await page.goto("/404.html");
  await expect(page.getByRole("heading", { level: 1 })).toContainText("This page could not be found");
  await expect(page.locator(".not-found").getByRole("link", { name: "How to start", exact: true })).toBeVisible();
});


for (const viewport of [{ width: 1280, height: 800 }, { width: 390, height: 844 }]) {
  test(`Windows SSH installation copies the complete recovery command at ${viewport.width}px`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await page.addInitScript(() => {
      Object.defineProperty(navigator, "clipboard", { configurable: true, value: {
        writeText: async (text: string) => { sessionStorage.setItem("test-copied-command", text); },
      } });
    });
    const base = localContext();
    const context = localContext({ repository: { ...base.repository, passport: {
      ...base.repository.passport, platform: "windows", missions: ["euler-access-ssh"],
    } } });
    await page.route("**/__passport/v1/context", (route) => route.fulfill({ json: context }));
    await page.route("**/__passport/v1/state", (route) => route.fulfill({ json: { status: "saved" } }));
    await page.goto("/missions/euler-access-ssh/");
    await expect(page.locator("[data-runtime-label]")).toHaveText("Your local passport");
    const step = page.locator("[data-recipe-step]").filter({
      has: page.getByRole("heading", { name: "Install only the public key", exact: true }),
    });
    const recipe = JSON.parse(readFileSync(new URL("../../curriculum/missions/euler-access-ssh/mission.json", import.meta.url), "utf8"));
    const command = recipe.activity.recipe.steps.find((step: { id: string }) => step.id === "install-public")
      .commands.find((command: { platform: string }) => command.platform === "windows").command;
    await checkScrolledCopy(page, step.locator('[data-command-platform="windows"]'), command);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await step.getByRole("link", { name: "Recover a failed Windows public-key installation" }).click();
    await expect(page.getByRole("heading", { name: "Windows Public-Key Installation Reports grep Errors" })).toBeVisible();
    await expect(page.locator("main")).toContainText("does not duplicate an exact key line");
  });
}

test("introductory definitions preserve spaces around emphasized words", async ({ page }) => {
  await page.goto("/getting-started/");
  await page.locator("[data-basic-words] summary").press("Enter");
  const definitions = page.locator("[data-basic-words] > p");
  await expect(definitions).toHaveText(
    "An application, or app, is a program you open, such as a web browser or PowerShell. " +
    "A browser is the app used to open websites, such as Firefox, Chrome, Edge, or Safari. " +
    "A file stores one item; a folder groups files. To download is to copy something from a website " +
    "to this computer. To install is to add an application so this computer can run it.",
    { useInnerText: true },
  );
});

for (const viewport of copyViewports) {
  test(`command Copy stays reachable and exact on ${viewport.name}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await page.addInitScript((zoom) => {
      document.addEventListener("DOMContentLoaded", () => { document.documentElement.style.zoom = String(zoom); });
      // Capture the exact clipboard API argument across engines without granting
      // browser-specific permissions. A separate Chromium test uses the real API.
      Object.defineProperty(navigator, "clipboard", { configurable: true, value: {
        writeText: async (text: string) => { sessionStorage.setItem("test-copied-command", text); },
      } });
    }, viewport.zoom);
    const base = localContext();
    const context = localContext({ repository: { ...base.repository, passport: {
      ...base.repository.passport, platform: "macos", missions: ["euler-access-ssh"],
    } } });
    await page.route("**/__passport/v1/context", (route) => route.fulfill({ json: context }));
    await page.route("**/__passport/v1/state", (route) => route.fulfill({ json: { status: "saved" } }));
    await page.goto("/missions/euler-access-ssh/");
    await expect(page.locator("[data-runtime-label]")).toHaveText("Your local passport");
    const step = page.locator("[data-recipe-step]").filter({
      has: page.getByRole("heading", { name: "Install only the public key", exact: true }),
    });
    const recipe = JSON.parse(readFileSync(new URL("../../curriculum/missions/euler-access-ssh/mission.json", import.meta.url), "utf8"));
    const expected = recipe.activity.recipe.steps.find((step: { id: string }) => step.id === "install-public")
      .commands.find((command: { platform: string }) => command.platform === "macos").command;
    await checkScrolledCopy(page, step.locator('[data-command-platform="macos"]'), expected);

    context.repository.passport.missions = ["git-manual-pr"];
    const practicePath = "/tmp/" + "fictional practice folder/".repeat(8) + "practice";
    await page.route("**/__passport/v2/practice", (route) => route.fulfill({ json: {
      status: "ready", path: practicePath, branch: "practice/student",
    } }));
    await page.goto("/missions/git-manual-pr/");
    await page.getByRole("button", { name: "Prepare practice folder" }).click();
    await expect(page.getByRole("button", { name: "Practice folder ready" })).toBeDisabled();
    await checkScrolledCopy(page, page.locator("[data-practice-commands] li"), `cd -- "${practicePath}"`);
    await checkScrolledCopy(page, page.locator("[data-practice-publish-commands] li").last(),
      'gh pr create --repo student/passport-exercises --base main --head practice/student --draft --title "docs(practice): complete manual Git loop" --body "Fictional IDEAL Lab Passport practice. No credentials, private data, or research data."');
    if (viewport.zoom === 1) {
      // CSS zoom can include background decoration in the document's scroll
      // bounds; the zoom case checks the actual command/control geometry above.
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    }
  });

  test(`attestation controls survive scrolling and feedback on ${viewport.name}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await page.addInitScript((zoom) => {
      document.addEventListener("DOMContentLoaded", () => { document.documentElement.style.zoom = String(zoom); });
    }, viewport.zoom);
    const base = localContext();
    const context = localContext({ repository: { ...base.repository, passport: {
      ...base.repository.passport, missions: ["core-orientation"],
    } } });
    await page.route("**/__passport/v1/context", (route) => route.fulfill({ json: context }));
    await page.route("**/__passport/v1/state", async (route) => {
      context.local_state = route.request().postDataJSON();
      await route.fulfill({ json: { status: "saved" } });
    });
    let releaseCheck = () => {};
    const pendingCheck = new Promise<void>((resolve) => { releaseCheck = resolve; });
    await page.route("**/__passport/v2/verify", async (route) => {
      await pendingCheck;
      await route.fulfill({ json: { status: "needs_work", score_percent: 100, critical_passed: true,
        receipt: { checks: { synthetic_check: false }, passed: false },
        feedback: [{ question: "command_place", correct: true, message: "Synthetic feedback for layout testing." }],
      } });
    });
    await page.goto("/missions/core-orientation/");
    const answer = page.locator('[data-question="command_place"] input[value="wrong_a"]');
    await answer.check();
    await expect.poll(() => context.local_state.mission_drafts).toHaveProperty("core-orientation.command_place", ["wrong_a"]);
    const attestations = page.locator(".mission-attestation input");
    await expect(attestations).toHaveCount(3);
    for (const input of await attestations.all()) await input.check();
    const inspect = async () => {
      for (const input of await attestations.all()) {
        await centerControl(input);
        await expect(input).toBeChecked();
        await expect(input).toBeEnabled();
      }
    };
    for (const fraction of [0, 1, 0.5, 0]) {
      await page.evaluate((fraction) => window.scrollTo(0, document.documentElement.scrollHeight * fraction), fraction);
      await inspect();
    }
    await page.getByRole("button", { name: "Check my work" }).click();
    await expect(page.getByRole("button", { name: "Check my work" })).toBeDisabled();
    await inspect();
    releaseCheck();
    await expect(page.getByText("Finish the checks on this computer")).toBeVisible();
    await inspect();
    await page.setViewportSize({ width: viewport.width === 390 ? 1280 : 390, height: 844 });
    await inspect();
    await page.reload();
    await expect(answer).toBeChecked();
    for (const input of await attestations.all()) {
      await input.scrollIntoViewIfNeeded();
      await expectReachable(input);
      await expect(input).toBeEnabled();
      // The app saves lesson answers, while browsers can restore checkbox
      // state on reload (observed in Firefox). Verify visibility and usability
      // without treating that browser behavior as the reported disappearance.
      await input.uncheck();
      await input.check();
      await expect(input).toBeChecked();
    }
  });
}

test("Copy remains reachable when automatic scrolling stops too early", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.addInitScript(() => {
    document.addEventListener("DOMContentLoaded", () => { document.documentElement.style.zoom = "2"; });
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: {
      writeText: async (text: string) => { sessionStorage.setItem("test-copied-command", text); },
    } });
    const nativeScroll = Element.prototype.scrollIntoView;
    Element.prototype.scrollIntoView = function (options) {
      nativeScroll.call(this, options);
      if (this.matches("[data-copy-command]")) window.scrollBy(0, -innerHeight);
    };
  });
  await page.goto("/missions/euler-access-ssh/");
  await page.getByRole("button", { name: "macOS", exact: true }).click();
  const step = page.locator("[data-recipe-step]").filter({
    has: page.getByRole("heading", { name: "Install only the public key", exact: true }),
  });
  const recipe = JSON.parse(readFileSync(new URL("../../curriculum/missions/euler-access-ssh/mission.json", import.meta.url), "utf8"));
  const expected = recipe.activity.recipe.steps.find((step: { id: string }) => step.id === "install-public")
    .commands.find((command: { platform: string }) => command.platform === "macos").command;
  await checkScrolledCopy(page, step.locator('[data-command-platform="macos"]'), expected);
});

test("command Copy writes exact multiline text through the browser clipboard", async ({ page, context, browserName }) => {
  test.skip(browserName !== "chromium", "clipboard permission automation is Chromium-specific; argument fidelity is checked in every engine");
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.goto("/missions/git-tools-auth/");
  await page.getByRole("button", { name: "macOS", exact: true }).click();
  const card = page.locator('[data-command-platform="macos"]').filter({ hasText: "git config" }).first();
  const expected = await card.locator("code").textContent();
  expect(expected).toContain("\n");
  await card.locator("[data-copy-command]").click();
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(expected);
});

const computerContext = (platform = "linux", missions = ["git-tools-auth", "python-environment", "euler-access-ssh", "git-manual-pr"]) => {
  const base = localContext();
  return localContext({
    repository: { ...base.repository, passport: { ...base.repository.passport, platform: "macos", missions } },
    computer: { platform, original_platform: "macos", setup_missions: missions.filter(id => ["git-tools-auth", "python-environment", "euler-access-ssh"].includes(id)) },
    local_state: { ...base.local_state, last_opened_mission: "git-manual-pr", attempt_counts: { "git-tools-auth": 3 }, mission_drafts: { "git-tools-auth": { saved: ["original"] } } },
    official: {
      source: "github_controller_check", remote_head_sha: "b".repeat(40),
      status: { schema_version: 2, curriculum_version: fixtureCurriculum, head_sha: "b".repeat(40), assessed_at: "2026-09-21T12:00:00Z", current_mission: "git-manual-pr", stage: "working", missions: missions.map(id => ({ id, status: id === "git-manual-pr" ? "needs_work" : "passed" })) },
    },
  });
};

for (const viewport of [{ name: "desktop", width: 1280, height: 900 }, { name: "mobile", width: 390, height: 844 }]) {
  test(`computer setup preserves assessment state and uses launcher OS on ${viewport.name}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    // Browser hints and the frozen registration must not override the launcher.
    await page.addInitScript(() => {
      Object.defineProperty(navigator, "platform", { configurable: true, get: () => "Win32" });
      Object.defineProperty(navigator, "userAgentData", { configurable: true, get: () => ({ platform: "Windows" }) });
    });
    const context = computerContext();
    const original = JSON.stringify(context);
    const writes: string[] = [];
    let checks = 0;
    await page.route("**/__passport/**", async route => {
      const path = new URL(route.request().url()).pathname;
      if (path.endsWith("/context")) return route.fulfill({ json: context });
      if (route.request().method() !== "GET") writes.push(path);
      if (path.endsWith("/computer-check")) {
        expect(route.request().postDataJSON()).toEqual({ mission: "git-tools-auth" });
        checks++;
        return route.fulfill({ json: { mission: "git-tools-auth", assessment_changed: false, platform: "linux", passed: checks > 1, checks: { git_installed: true, gh_authenticated: checks > 1 } } });
      }
      if (path.endsWith("/practice")) return route.fulfill({ json: { status: "ready", path: "/home/student/IDEAL passport/practice", branch: "practice/student" } });
      return route.fulfill({ status: 503, json: {} });
    });
    await page.goto("/passport/");
    await expect(page.locator("[data-platform]")).toHaveText("Linux on this computer");
    await expect(page.locator("[data-computer-banner]")).toContainText("You're now using Linux");
    await page.locator('[data-computer-banner] a[href="/computer/"]').click();
    await expect(page.locator("[data-computer-summary]")).toHaveText("This computer runs Linux");
    await expect(page.locator("[data-linux-intro]")).toBeHidden();
    await page.getByText("Optional refresher for a new operating system", { exact: true }).press("Enter");
    await expect(page.locator("[data-linux-intro]")).toBeVisible();
    await expect(page.locator("[data-computer-mission]:visible")).toHaveCount(3);
    await assertAccessible(page);
    await page.locator('[data-computer-mission="git-tools-auth"] a').click();
    await expect(page.locator("[data-recheck-computer]")).toBeEnabled();
    await expect(page.locator("[data-submit-mission]")).toBeHidden();
    await expect(page.locator("[data-check-work]")).toBeHidden();
    await expect(page.locator("[data-mission-footer]")).toBeHidden();
    await expect(page.locator(".mission-contract")).toBeHidden();
    await expect(page.locator("[data-work-here]")).toHaveText("On your Linux computer");
    await page.locator(".recipe-command-guide summary").click();
    await expect(page.locator('[data-command-guide-platform="linux"]')).toBeVisible();
    await expect(page.locator('[data-command-guide-platform="macos"]')).toBeHidden();
    await page.getByRole("button", { name: "Prepare practice folder" }).click();
    await expect(page.locator("[data-practice-commands] code")).toHaveText('cd -- "/home/student/IDEAL passport/practice"');
    await page.getByRole("button", { name: "Check this computer" }).click();
    await expect(page.locator("[data-computer-check-status]")).toContainText("Some computer checks need attention");
    await page.getByRole("button", { name: "Check this computer" }).click();
    await expect(page.locator("[data-computer-check-status]")).toContainText("Computer checks passed. Your assessment progress is unchanged.");
    await assertAccessible(page);
    expect(writes.filter(path => !path.endsWith("/sync"))).toEqual(["/__passport/v2/practice", "/__passport/v2/computer-check", "/__passport/v2/computer-check"]);
    expect(JSON.stringify(context)).toBe(original);
    // The ordinary assessed lesson is still locked after the recheck.
    await page.goto("/missions/git-tools-auth/");
    await expect(page.getByText("Lesson locked", { exact: true })).toBeVisible();
    await expect(page.locator("[data-check-work]")).toBeDisabled();
  });
}

for (const platform of ["macos", "windows"]) {
  test(`computer setup follows the launcher when returning to ${platform}`, async ({ page }) => {
    await page.route("**/__passport/v1/context", route => route.fulfill({ json: computerContext(platform) }));
    await page.goto("/computer/");
    await expect(page.locator("[data-linux-intro]")).toBeHidden();
    await expect(page.locator("[data-computer-summary]")).toHaveText(`This computer runs ${platform === "macos" ? "macOS" : "Windows"}`);
    await page.goto("/missions/python-environment/?setup=computer");
    await expect(page.locator("[data-recheck-computer]")).toBeEnabled();
    await page.locator(".recipe-command-guide summary").click();
    await expect(page.locator(`[data-command-guide-platform="${platform}"]`)).toBeVisible();
    await expect(page.locator('[data-command-guide-platform="linux"]')).toBeHidden();
    await expect(page.locator("[data-submit-mission]")).toBeHidden();
  });
}

test("computer setup offers only assigned diagnostics and SSH never claims a live login", async ({ page }) => {
  const context = computerContext("linux", ["euler-access-ssh", "git-manual-pr"]);
  const writes: string[] = [];
  let checks = 0;
  await page.route("**/__passport/**", async route => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/context")) return route.fulfill({ json: context });
    if (route.request().method() !== "GET") writes.push(path);
    if (path.endsWith("/computer-check")) {
      expect(route.request().postDataJSON()).toEqual({ mission: "euler-access-ssh" });
      if (++checks === 1) return route.fulfill({ status: 503, json: {} });
      return route.fulfill({ json: { mission: "euler-access-ssh", assessment_changed: false, passed: true, checks: { euler_alias: true } } });
    }
    return route.fulfill({ status: 503, json: {} });
  });
  await page.goto("/computer/");
  await expect(page.locator("[data-computer-mission]:visible")).toHaveCount(1);
  await page.locator('[data-computer-mission="euler-access-ssh"] a').click();
  await expect(page.locator("[data-computer-ssh-note]")).toContainText("cannot prove access to Euler");
  await page.getByRole("button", { name: "Check this computer" }).click();
  await expect(page.locator("[data-computer-check-status]")).toContainText("Setup check unavailable");
  await page.getByRole("button", { name: "Check this computer" }).click();
  await expect(page.locator("[data-computer-check-status]")).toContainText("this button did not connect to Euler");
  await page.goto("/missions/git-manual-pr/?setup=computer");
  await expect(page.locator("[data-computer-check-status]")).toContainText("cannot recheck this lesson");
  await expect(page.getByRole("button", { name: "Check this computer" })).toBeDisabled();
  await expect(page.locator("[data-submit-mission]")).toBeHidden();
  expect(writes).toEqual(["/__passport/v2/computer-check", "/__passport/v2/computer-check"]);
});

test("computer setup remains inactive in preview and with an older launcher", async ({ page }) => {
  await page.goto("/computer/");
  await expect(page.locator("[data-computer-lessons]")).toBeHidden();
  await page.goto("/missions/git-tools-auth/?setup=computer");
  await expect(page.getByRole("button", { name: "Check this computer" })).toBeDisabled();
  await expect(page.locator("[data-submit-mission]")).toBeHidden();
  await page.route("**/__passport/v1/context", route => route.fulfill({ json: localContext() }));
  await page.goto("/computer/");
  await expect(page.locator("[data-computer-intro]")).toContainText("Update and reopen");
  await expect(page.locator("[data-computer-lessons]")).toBeHidden();
});


test("Python integrated help preserves a saved draft and does not check or submit", async ({ page }) => {
  const base = localContext();
  let context = localContext({
    repository: { ...base.repository, passport: { ...base.repository.passport, missions: ["python-environment", "python-reproducible-change"] } },
    official: { status: { current_mission: "python-reproducible-change", head_sha: "b".repeat(40), stage: "in_progress" } },
    local_state: {
      ...base.local_state,
      last_opened_mission: "python-reproducible-change",
      mission_drafts: { "python-reproducible-change": { test_claim: ["correct"], reproducible_scope: ["wrong_a"] } },
      attempt_counts: { "python-reproducible-change": 2 },
    },
  });
  let writes = 0;
  await page.route("**/__passport/v1/context", route => route.fulfill({ contentType: "application/json", body: JSON.stringify(context) }));
  await page.route("**/__passport/v1/sync", route => route.fulfill({ status: 503, body: "{}" }));
  await page.route("**/__passport/v1/state", async route => {
    writes++; context = { ...context, local_state: route.request().postDataJSON() };
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ status: "saved_locally_not_completion" }) });
  });
  for (const endpoint of ["verify", "submit"]) {
    await page.route(`**/__passport/v2/${endpoint}`, route => { writes++; return route.fulfill({ status: 500, body: "{}" }); });
  }
  await page.goto("/missions/python-reproducible-change/");
  await expect(page.locator('[data-question="test_claim"] input[value="correct"]')).toBeChecked();
  const before = structuredClone(context);
  const writesBefore = writes;
  await page.locator('#python-reading-code > summary').click();
  if (fixtureCurriculum === "2.1.2") await page.locator('#step-fix .command-disclosure summary').click();
  else await expect(page.locator('#step-fix .command-disclosure')).toHaveCount(0);
  const extra = page.locator('#python-extra-practice');
  await extra.locator(':scope > summary').click();
  await extra.locator('details').last().locator('summary').click();
  expect(context).toEqual(before);
  expect(writes).toBe(writesBefore);
  await expect(page.getByRole('button', { name: 'Submit lesson', exact: true })).toBeDisabled();
  await page.reload();
  await expect(page.locator('[data-question="test_claim"] input[value="correct"]')).toBeChecked();
  await expect(page.locator('[data-question="reproducible_scope"] input[value="wrong_a"]')).toBeChecked();
  expect(context.local_state.attempt_counts).toEqual({ "python-reproducible-change": 2 });
});
