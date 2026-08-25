import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import { AdminUIReportTemplates, reportTemplateFieldValue, reportTemplateFields } from "./admin-ui-report-templates";

describe("AdminUIReportTemplates", () => {
  it("renders report template contributions and runs registered actions", async () => {
    const user = userEvent.setup();
    const data = emptyData();
    data.summary.total_tokens = 1200;
    data.pluginUI = [
      {
        plugin_id: "tokenhub.report.executive",
        id: "monthly-board",
        slot: "report.template",
        title: "Monthly Board Report",
        action: "report.monthly.render",
        schema: {
          fields: [
            { name: "tokens", type: "metric", label: "Token volume", source: "summary.total_tokens", format: "compact" },
            { name: "period", type: "text", label: "Period", value: "month" },
          ],
        },
      },
      {
        plugin_id: "tokenhub.admin.plugin-ecosystem",
        id: "runtime",
        slot: "settings.panel",
        title: "Plugin Runtime",
      },
    ];
    data.pluginActions = [{
      plugin_id: "tokenhub.report.executive",
      action_id: "report.monthly.render",
      kind: "import_export",
      capability: "report.render",
      subject: "usage",
    }];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { status: "ready", download_token: "secret-token" },
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    render(<AdminUIReportTemplates api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);

    expect(screen.getByText("报表模板")).toBeInTheDocument();
    expect(screen.getByText("Monthly Board Report")).toBeInTheDocument();
    expect(screen.getByText("Token volume")).toBeInTheDocument();
    expect(screen.getByText("Period")).toBeInTheDocument();
    expect(screen.queryByText("Plugin Runtime")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /运行报表模板/ }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.report.executive/actions/report.monthly.render");
    expect(JSON.parse(String(init.body))).toEqual({ source: "report.template", contribution_id: "monthly-board" });
    await waitFor(() => expect(screen.getByText(/\[redacted\]/)).toBeInTheDocument());
    expect(screen.queryByText("secret-token")).not.toBeInTheDocument();
  });

  it("formats text, metric, and code viewer fields", () => {
    const data = emptyData();
    data.summary.estimated_cost_usd = 8.5;
    data.breakdown.models = [{ id: "gpt", request_count: 1, input_tokens: 2, output_tokens: 3, total_tokens: 5, estimated_cost_usd: 0.01 }];
    const fields = reportTemplateFields({
      plugin_id: "tokenhub.report.finance",
      id: "cost-pack",
      slot: "report.template",
      schema: {
        fields: [
          { name: "cost", type: "metric", label: "Cost", source: "summary.estimated_cost_usd", format: "money_usd" },
          { name: "dataset", type: "text", label: "Dataset", value: "usage" },
          { name: "raw", type: "code_viewer", label: "Raw", source: "breakdown.models.0" },
          { name: "ignored", type: "table", label: "Ignored" },
        ],
      },
    });

    expect(fields).toHaveLength(3);
    expect(reportTemplateFieldValue(data, fields[0])).toBe("$8.50");
    expect(reportTemplateFieldValue(data, fields[1])).toBe("usage");
    expect(reportTemplateFieldValue(data, fields[2])).toContain("gpt");
  });
});
