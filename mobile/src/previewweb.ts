// Preview-only: inside the native shell (a Simulator capture of `preview.html`) the real plugin
// proxy would answer instead of the scripted Mac, so report every plugin as unavailable. Runs first.
import { Capacitor } from "@capacitor/core";

if (new URLSearchParams(location.search).has("bare")) {
  (Capacitor as { isPluginAvailable: (n: string) => boolean }).isPluginAvailable = () => false;
}
