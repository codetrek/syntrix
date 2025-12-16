import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react()],
  define: {
    global: 'window', // RxDB needs global to be defined
  },
  server: {
    port: 3000,
    strictPort: true,
  },
})
