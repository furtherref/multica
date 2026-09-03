// @vitest-environment node

import { describe, expect, it } from "vitest";
import { runtimeKeys, runtimeUsageCoverageOptions } from "./queries";

describe("runtime usage coverage query", () => {
  it("keys coverage by runtime, period, and viewing timezone", () => {
    expect(runtimeKeys.usageCoverage("runtime-1", 30, "Asia/Shanghai")).toEqual([
      "runtimes",
      "usage",
      "coverage",
      "runtime-1",
      30,
      "Asia/Shanghai",
    ]);
    expect(
      runtimeUsageCoverageOptions("runtime-1", 30, "Asia/Shanghai").queryKey,
    ).toEqual([
      "runtimes",
      "usage",
      "coverage",
      "runtime-1",
      30,
      "Asia/Shanghai",
    ]);
  });

  it("keys the budget by runtime id only (periods are UTC, no tz)", () => {
    expect(runtimeKeys.budget("rt-1")).toEqual(["runtimes", "budget", "rt-1"]);
  });
});
