import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import ProjectsResize from './ProjectsResize.svelte';
import { nav } from '$lib/nav.svelte';
import { PROJECTS_DEFAULT, PROJECTS_MAX, PROJECTS_MIN } from '$lib/sidebarwidth';

afterEach(() => { cleanup(); nav.setProjectsWidth(PROJECTS_DEFAULT); });

describe('projects column divider', () => {
  it('resizes with the keyboard within its bounds, persists, and resets on double click', async () => {
    nav.sidebarOpen = true;
    render(ProjectsResize, { width: nav.projectsWidth, maxWidth: 400 });
    const handle = screen.getByRole('separator', { name: 'Resize projects column' });
    await fireEvent.keyDown(handle, { key: 'ArrowRight' });
    expect(nav.projectsWidth).toBe(PROJECTS_DEFAULT + 10);
    expect(localStorage.getItem('lola.sidebar.projects')).toBe(String(PROJECTS_DEFAULT + 10));
    await fireEvent.keyDown(handle, { key: 'End' });
    expect(nav.projectsWidth).toBe(400);
    await fireEvent.keyDown(handle, { key: 'Home' });
    expect(nav.projectsWidth).toBe(PROJECTS_MIN);
    await fireEvent.dblClick(handle);
    expect(nav.projectsWidth).toBe(PROJECTS_DEFAULT);
  });
  it('never stores a width outside the allowed range', () => {
    nav.setProjectsWidth(5000);
    expect(nav.projectsWidth).toBe(PROJECTS_MAX);
    nav.setProjectsWidth(10);
    expect(nav.projectsWidth).toBe(PROJECTS_MIN);
  });
});
