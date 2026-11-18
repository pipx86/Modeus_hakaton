/**
 * @file main.tsx
 * @description Точка входа: монтирование React-приложения в DOM.
 */

import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { ThemeProvider } from 'next-themes'
import { Toaster } from '@/components/ui/sonner'
import { frontendLog, installFrontendLogHandlers } from '@/lib/frontend-log'
import './index.css'
import App from './App.tsx'

installFrontendLogHandlers()
frontendLog.info('frontend started', { url: window.location.href })

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeProvider attribute="class" defaultTheme="dark" enableSystem={false} storageKey="eduagent-theme">
      <App />
      <Toaster />
    </ThemeProvider>
  </StrictMode>,
)
