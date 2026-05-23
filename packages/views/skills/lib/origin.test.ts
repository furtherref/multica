import { describe, expect, it } from "vitest";

import { readOrigin } from "./origin";

describe("readOrigin", () => {
  it("preserves uploaded bundle origins", () => {
    expect(
      readOrigin({
        config: {
          origin: {
            type: "uploaded_bundle",
            label: "team.zip/review-helper",
          },
        },
      } as never),
    ).toEqual({
      type: "uploaded_bundle",
      label: "team.zip/review-helper",
    });
  });
});
