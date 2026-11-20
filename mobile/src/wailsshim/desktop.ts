// The module `@bindings/desktop` resolves to: one namespace per generated
// Wails service, mirroring the generated `index.ts` exactly. A method here is
// forwarded to the daemon, answered on the phone, or rejected with
// `UnsupportedOnMobileError`; `services/*.ts` carries the route of each, and
// `conformance.ts` fails the type check when this barrel drifts from the bindings.
import * as ConfigService from "./services/config";
import * as ContextService from "./services/context";
import * as DaemonService from "./services/daemon";
import * as DoctorService from "./services/doctor";
import * as HubService from "./services/hub";
import * as LinearService from "./services/linear";
import * as PhonesService from "./services/phones";
import * as PreviewService from "./services/preview";
import * as ProjectGitService from "./services/projectgit";
import * as SandboxLoginService from "./services/sandboxlogin";
import * as SSHService from "./services/ssh";
import * as TalkService from "./services/talk";
import * as TermService from "./services/term";
import * as UpdateService from "./services/update";

export {
  ConfigService,
  ContextService,
  DaemonService,
  DoctorService,
  HubService,
  LinearService,
  PhonesService,
  PreviewService,
  ProjectGitService,
  SandboxLoginService,
  SSHService,
  TalkService,
  TermService,
  UpdateService,
};

export type * from "@bindings/desktop/models";
export type { PaneCloseData, PaneInfo, PaneKind, PanesData, ShellCreateData } from "../wire";
