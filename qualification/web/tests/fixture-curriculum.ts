import { readFileSync } from "node:fs";

// Use the contract actually served by the build, including pinned legacy runs.
export const fixtureCurriculum: string = JSON.parse(readFileSync(
  new URL("../dist/data/routes.json",import.meta.url),"utf8",
)).curriculum_version;
