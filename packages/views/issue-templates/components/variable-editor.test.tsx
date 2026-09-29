import { describe, expect, it } from "vitest";
import { variablesFromConfig, variablesToConfig } from "./variable-editor";

describe("variablesFromConfig", () => {
  it("extracts an empty list from a config without variables", () => {
    expect(variablesFromConfig({})).toEqual([]);
  });

  it("maps config.variables entries to drafts", () => {
    const result = variablesFromConfig({
      variables: {
        area: { label: "What area?", required: true, default: "billing" },
        module: { label: "Module" },
      },
    });
    expect(result).toEqual([
      { key: "area", label: "What area?", required: true, defaultValue: "billing" },
      { key: "module", label: "Module", required: false, defaultValue: "" },
    ]);
  });

  it("coerces a non-string default to string", () => {
    const result = variablesFromConfig({
      variables: { count: { default: 3 } },
    });
    expect(result).toEqual([{ key: "count", label: "", required: false, defaultValue: "3" }]);
  });
});

describe("variablesToConfig", () => {
  it("omits empty keys and empty definitions", () => {
    const result = variablesToConfig([
      { key: "", label: "", required: false, defaultValue: "" },
      { key: "  ", label: "x", required: false, defaultValue: "" },
    ]);
    expect(result).toEqual({});
  });

  it("serializes a full draft, keeping bare declarations as empty objects", () => {
    const result = variablesToConfig([
      { key: "area", label: "What area?", required: true, defaultValue: "billing" },
      { key: "module", label: "", required: false, defaultValue: "" },
    ]);
    expect(result).toEqual({
      area: { label: "What area?", required: true, default: "billing" },
      module: {},
    });
  });

  it("round-trips through variablesFromConfig", () => {
    const drafts = [
      { key: "area", label: "Area", required: true, defaultValue: "billing" },
    ];
    expect(variablesFromConfig({ variables: variablesToConfig(drafts) })).toEqual(drafts);
  });
});
