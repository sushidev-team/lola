<script lang="ts">
  import { onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { store } from "$lib/store.svelte";
  import { updates } from "$lib/update.svelte";
  import { nav } from "$lib/nav.svelte";
  import VitalsBar from "$lib/components/VitalsBar.svelte";
  import Footer from "$lib/components/Footer.svelte";
  import Cockpit from "$lib/views/Cockpit.svelte";
  import Home from "$lib/views/Home.svelte";
  import ProjectDetail from "$lib/views/ProjectDetail.svelte";
  import PRPicker from "$lib/views/PRPicker.svelte";
  import TicketPicker from "$lib/views/TicketPicker.svelte";
  import DoctorOverlay from "$lib/views/DoctorOverlay.svelte";
  import SettingsForm from "$lib/views/SettingsForm.svelte";
  import ProjectForm from "$lib/views/ProjectForm.svelte";
  import UpdateOverlay from "$lib/views/UpdateOverlay.svelte";
  import Setup from "$lib/views/Setup.svelte";

  onMount(() => {
    store.start();
    // Load the version + run the interval-gated startup auto-check so the footer
    // can surface a badge when a release is out.
    void updates.init();
    // The macOS status-bar menu cannot open an overlay itself — it is nav state
    // that lives here — so it asks. See newStatusBarMenu in main.go.
    Events.On("app:open-settings", () => nav.openOverlay("settings"));
    Events.On("app:open-update", () => nav.openOverlay("update"));
  });

  function typing(el: EventTarget | null): boolean {
    const t = el as HTMLElement | null;
    // SELECT is included so a picker/select dropdown swallows global shortcuts
    // the same way a text field does (arrow keys pick options, letters filter).
    return (
      !!t &&
      (t.tagName === "INPUT" ||
        t.tagName === "TEXTAREA" ||
        t.tagName === "SELECT" ||
        t.isContentEditable)
    );
  }

  function onKey(e: KeyboardEvent) {
    if (typing(e.target)) return;
    if (nav.overlay) {
      if (e.key === "Escape") nav.closeOverlay();
      return;
    }
    // A focused live terminal swallows shortcuts (handled inside the view).
    if (nav.focusedTerm) return;

    switch (e.key) {
      case "p":
        nav.goHome();
        break;
      case "d":
        nav.openOverlay("doctor");
        break;
      case "S":
        nav.openOverlay("settings");
        break;
      case "Escape":
        if (nav.view !== "cockpit") nav.goCockpit(nav.scoped ? nav.project : "");
        else if (nav.scoped) nav.goCockpit("");
        break;
    }
  }
</script>

<svelte:window on:keydown={onKey} />

{#if store.configChecked && !store.hasConfig}
  <div class="h-full bg-canvas text-ink">
    <div class="drag h-11 shrink-0"></div>
    <Setup />
  </div>
{:else}
<div class="flex h-full flex-col bg-canvas text-ink">
  <VitalsBar />

  <main class="min-h-0 flex-1 overflow-hidden">
    {#if nav.view === "cockpit"}
      <Cockpit />
    {:else if nav.view === "home"}
      <Home />
    {:else if nav.view === "detail"}
      <ProjectDetail />
    {:else if nav.view === "prpicker"}
      <PRPicker />
    {:else if nav.view === "ticketpicker"}
      <TicketPicker />
    {/if}
  </main>

  <Footer />
</div>

{#if nav.overlay === "doctor"}
  <DoctorOverlay />
{:else if nav.overlay === "settings"}
  <SettingsForm />
{:else if nav.overlay === "project"}
  <ProjectForm />
{:else if nav.overlay === "update"}
  <UpdateOverlay />
{/if}
{/if}
