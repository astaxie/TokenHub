import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { PluginActionRunner, PluginBackgroundJobRunner, type PluginInputRunnerDraft } from "./plugin-action-runner";

describe("PluginActionRunner", () => {
  it("renders supported fields, forwards updates, and submits the descriptor", () => {
    const action = {
      plugin_id: "tokenhub.provider.kimi",
      action_id: "configure",
      kind: "mutate",
      input_schema: {
        type: "object",
        required: ["name"],
        properties: {
          name: { type: "string" },
          enabled: { type: "boolean" },
          retries: { type: "integer" },
        },
      },
    };
    const draft: PluginInputRunnerDraft = {
      values: { name: "alpha", enabled: false, retries: "" },
      busy: false,
      error: "",
      result: '{\n  "access_token": "[redacted]"\n}',
    };
    const onChange = vi.fn();
    const onSubmit = vi.fn();

    render(
      <PluginActionRunner
        action={action}
        draft={draft}
        labels={{ submit: "Run", submitting: "Running", unsupportedSchema: "Unsupported schema" }}
        onChange={onChange}
        onSubmit={onSubmit}
      />,
    );

    expect(screen.getByRole("textbox", { name: /name/ })).toHaveValue("alpha");
    expect(screen.getByRole("spinbutton", { name: /retries/ })).toBeInTheDocument();
    expect(
      screen.getByText((_, node) => node?.tagName === "PRE" && node.textContent?.includes('"access_token": "[redacted]"') === true),
    ).toBeInTheDocument();

    fireEvent.change(screen.getByRole("textbox", { name: /name/ }), { target: { value: "beta" } });
    fireEvent.click(screen.getByRole("checkbox", { name: /enabled/ }));
    fireEvent.click(screen.getByRole("button", { name: "Run" }));

    expect(onChange).toHaveBeenCalledWith(action, "name", "beta");
    expect(onChange).toHaveBeenCalledWith(action, "enabled", true);
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it("renders supported fields and keeps the runner disabled for unsupported schemas", () => {
    const job = {
      plugin_id: "tokenhub.jobs",
      job_id: "refresh.quota",
      schedule: "*/10 * * * *",
      max_concurrency: 1,
      input_schema: {
        type: "object",
        properties: {
          target: { type: "string" },
          extras: { type: "array" },
        },
      },
    };
    const draft: PluginInputRunnerDraft = {
      values: { target: "primary" },
      busy: false,
      error: "",
      result: "",
    };
    const onChange = vi.fn();
    const onSubmit = vi.fn();

    render(
      <PluginBackgroundJobRunner
        draft={draft}
        job={job}
        labels={{ submit: "Run job", submitting: "Running job", unsupportedSchema: "Unsupported schema" }}
        onChange={onChange}
        onSubmit={onSubmit}
      />,
    );

    expect(screen.getByRole("textbox", { name: /target/ })).toHaveValue("primary");
    expect(screen.queryByRole("textbox", { name: /extras/ })).not.toBeInTheDocument();
    expect(screen.getByText("Unsupported schema")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Run job" })).toBeDisabled();

    fireEvent.click(screen.getByRole("button", { name: "Run job" }));
    expect(onChange).not.toHaveBeenCalled();
    expect(onSubmit).not.toHaveBeenCalled();
  });
});
