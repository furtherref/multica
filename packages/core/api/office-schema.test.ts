import { describe, it, expect } from "vitest";
import { OfficeConfigResponseSchema, EMPTY_OFFICE_CONFIG } from "./schemas";
import { parseWithFallback } from "./schema";

const opts = { endpoint: "GET /api/attachments/{id}/office-config" };

describe("OfficeConfigResponseSchema", () => {
  it("parses a valid response", () => {
    const r = parseWithFallback(
      { document_server_url: "https://weboffice.x", config: { documentType: "word", token: "jwt" } },
      OfficeConfigResponseSchema,
      EMPTY_OFFICE_CONFIG,
      opts,
    );
    expect(r.document_server_url).toBe("https://weboffice.x");
    expect(r.config.documentType).toBe("word");
  });

  it("falls back when document_server_url is missing", () => {
    const r = parseWithFallback({ config: {} }, OfficeConfigResponseSchema, EMPTY_OFFICE_CONFIG, opts);
    expect(r).toBe(EMPTY_OFFICE_CONFIG);
  });

  it("falls back on a null body", () => {
    const r = parseWithFallback(null, OfficeConfigResponseSchema, EMPTY_OFFICE_CONFIG, opts);
    expect(r).toBe(EMPTY_OFFICE_CONFIG);
  });
});
