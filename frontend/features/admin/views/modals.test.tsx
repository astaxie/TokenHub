import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import { APIKeyWizardModal } from "./modals";

describe("APIKeyWizardModal", () => {
  it("collects project, purpose, model scope, and limits before issuing a key", async () => {
    const user = userEvent.setup();
    const onCreate = vi.fn();
    const currentUser = {
      id: "usr_admin",
      username: "admin",
      name: "Admin",
      email: "admin@example.com",
      role: "admin",
      status: "active",
    };
    const data = emptyData();
    data.users = [currentUser];
    data.projects = [{
      id: "prj_quality",
      name: "Quality Project",
      owner_user_id: currentUser.id,
      status: "active",
    }];

    render(
      <APIKeyWizardModal
        data={data}
        currentUser={currentUser}
        initialValues={{ project_id: "prj_quality", owner_user_id: currentUser.id }}
        loading={false}
        onClose={vi.fn()}
        onCreate={onCreate}
      />,
    );

    await user.click(screen.getByRole("button", { name: "下一步" }));
    await user.type(screen.getByLabelText("Key 名称"), "quality-smoke-key");
    const groupInput = screen.getByLabelText("用途/环境");
    await user.clear(groupInput);
    await user.type(groupInput, "ci");
    await user.click(screen.getByRole("button", { name: "下一步" }));
    await user.click(screen.getByRole("button", { name: "下一步" }));
    await user.type(screen.getByRole("spinbutton", { name: /每分钟请求数/ }), "60");
    await user.click(screen.getByRole("button", { name: "下一步" }));
    await user.click(screen.getByRole("button", { name: "生成 Key" }));

    expect(onCreate).toHaveBeenCalledTimes(1);
    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({
      project_id: "prj_quality",
      owner_user_id: "usr_admin",
      name: "quality-smoke-key",
      group: "ci",
      rate_limit_rpm: "60",
      allowed_models: "",
      status: "active",
    }));
  });
});
