import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enIssues from "../../../locales/en/issues.json";
import { PillButton } from "../../../common/pill-button";
import { PriorityPicker } from "./priority-picker";

const TEST_RESOURCES = {
  en: { issues: enIssues },
};

function renderWithI18n(ui: React.ReactElement) {
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {ui}
    </I18nProvider>,
  );
}

describe("PriorityPicker trigger content", () => {
  // The create-issue dialog passes a bare childless PillButton as
  // triggerRender and relies on the picker's computed default content
  // (icon + label). The deferred lookalike trigger can't compute that
  // content, so such callers must render the real picker eagerly.
  it("shows the current priority in a childless triggerRender without interaction", () => {
    renderWithI18n(
      <PriorityPicker
        priority="none"
        onUpdate={() => {}}
        triggerRender={<PillButton />}
        align="start"
      />,
    );

    expect(screen.getByText("No priority")).toBeInTheDocument();
  });

  it("lets triggerRender children win over the computed default content", () => {
    renderWithI18n(
      <PriorityPicker
        priority="none"
        onUpdate={() => {}}
        triggerRender={
          <button type="button">
            <span>own content</span>
          </button>
        }
      />,
    );

    expect(screen.getByText("own content")).toBeInTheDocument();
    expect(screen.queryByText("No priority")).not.toBeInTheDocument();
  });
});
