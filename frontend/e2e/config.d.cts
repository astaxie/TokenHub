declare const e2eDefaults: Readonly<{
  frontendPort: number;
  backendPort: number;
  upstreamPort: number;
  adminIdentity: string;
  adminPassword: string;
  upstreamKey: string;
  nextDistDir: string;
}>;

export = e2eDefaults;
