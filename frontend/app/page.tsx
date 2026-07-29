import { AdminConsole } from "@/features/admin/shell/admin-console";
import { runtimeAPIBaseURL } from "@/lib/runtime-config";

export const dynamic = "force-dynamic";

export default function LoginPage() {
  return <AdminConsole defaultBaseURL={runtimeAPIBaseURL()} />;
}
