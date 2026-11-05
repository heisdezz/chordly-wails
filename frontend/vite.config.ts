import { defineConfig } from 'vite';
import solid from 'vite-plugin-solid';
import tailwindcss from '@tailwindcss/vite';

// Chordly frontend (TSK-001): SolidJS + TanStack Router + Tailwind 4 / DaisyUI 5.
// Kept minimal; TSK-201+ own src/lib and TSK-202 owns components.
export default defineConfig({
  plugins: [solid(), tailwindcss()],
});
