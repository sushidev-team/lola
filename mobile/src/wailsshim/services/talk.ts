// TalkService: one function per generated method, routed as ROUTES says.
import type * as Gen from "@bindings-generated/desktop";
import type * as P from "@bindings/internal/protocol";
import type * as M from "@bindings/desktop/models";
export { AttachClipboardImage, ReadAttachmentImage } from "./talkimage";
import { bridge } from "../bridge";
import { unsupported } from "../errors";
import { MAC_ONLY } from "./reasons";
import type { Routes } from "./routes";

/** PLATFORM. */
export function AttachImageFile(_worktree: string, _src: string): Promise<M.ImageAttachment> {
  return unsupported("TalkService.AttachImageFile", MAC_ONLY);
}

/** FORWARDED: cmd=agentChoose. */
export function Choose(a: P.AgentChooseArgs): Promise<P.AgentChooseData> {
  return bridge.request<P.AgentChooseData>("TalkService.Choose", "agentChoose", { args: a });
}

/** FORWARDED: cmd=endedSessions. */
export function Ended(): Promise<P.EndedSessionsData> {
  return bridge.request<P.EndedSessionsData>("TalkService.Ended", "endedSessions");
}

/** FORWARDED: cmd=agentFeed. */
export function Feed(a: P.AgentFeedArgs): Promise<P.AgentFeedData> {
  return bridge.request<P.AgentFeedData>("TalkService.Feed", "agentFeed", { args: a });
}

/** FORWARDED: cmd=agentFeedOutput. */
export function FeedOutput(a: P.AgentFeedOutputArgs): Promise<P.AgentFeedOutputData> {
  return bridge.request<P.AgentFeedOutputData>("TalkService.FeedOutput", "agentFeedOutput", { args: a });
}

/** FORWARDED: cmd=agentQueueRelease. */
export function QueueRelease(a: P.AgentQueueReleaseArgs): Promise<P.AgentSayData> {
  return bridge.request<P.AgentSayData>("TalkService.QueueRelease", "agentQueueRelease", { args: a });
}

/** FORWARDED: cmd=agentSay. */
export function Say(a: P.AgentSayArgs): Promise<P.AgentSayData> {
  return bridge.request<P.AgentSayData>("TalkService.Say", "agentSay", { args: a });
}

/** FORWARDED: cmd=agentSetting. */
export function Setting(a: P.AgentSettingArgs): Promise<P.AgentSettingData> {
  return bridge.request<P.AgentSettingData>("TalkService.Setting", "agentSetting", { args: a });
}

/** FORWARDED: cmd=talkState. */
export function State(a: P.TalkStateArgs): Promise<P.TalkStateData> {
  return bridge.request<P.TalkStateData>("TalkService.State", "talkState", { args: a });
}

/** FORWARDED: cmd=agentStop. */
export function Stop(a: P.AgentStopArgs): Promise<P.AgentStopData> {
  return bridge.request<P.AgentStopData>("TalkService.Stop", "agentStop", { args: a });
}

export const ROUTES: Routes<typeof Gen.TalkService> = {
  AttachClipboardImage: { kind: "fwd", cmd: "attachImage" },
  AttachImageFile: { kind: "platform", reason: MAC_ONLY },
  Choose: { kind: "fwd", cmd: "agentChoose" },
  Ended: { kind: "fwd", cmd: "endedSessions" },
  Feed: { kind: "fwd", cmd: "agentFeed" },
  FeedOutput: { kind: "fwd", cmd: "agentFeedOutput" },
  QueueRelease: { kind: "fwd", cmd: "agentQueueRelease" },
  ReadAttachmentImage: { kind: "fwd", cmd: "attachmentImage" },
  Say: { kind: "fwd", cmd: "agentSay" },
  Setting: { kind: "fwd", cmd: "agentSetting" },
  State: { kind: "fwd", cmd: "talkState" },
  Stop: { kind: "fwd", cmd: "agentStop" },
};
