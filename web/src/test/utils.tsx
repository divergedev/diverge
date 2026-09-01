import React, { ReactElement, useState } from 'react'
import { render, RenderOptions, renderHook as rtlRenderHook, RenderHookOptions } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ThemeProvider } from '@/components/ThemeProvider'
import { AuthProvider } from '@/hooks/useAuth'

import { MemoryRouter } from 'react-router-dom'

export const AllTheProviders = ({ children }: { children: React.ReactNode }) => {
  const [queryClient] = useState(() => new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  }))

  return (
    <MemoryRouter>
      <QueryClientProvider client={queryClient}>
        <ThemeProvider defaultTheme="dark">
          <AuthProvider>
            {children}
          </AuthProvider>
        </ThemeProvider>
      </QueryClientProvider>
    </MemoryRouter>
  )
}

const customRender = (
  ui: ReactElement,
  options?: Omit<RenderOptions, 'wrapper'>,
) => render(ui, { wrapper: AllTheProviders, ...options })

const customRenderHook = <Result, Props>(
  renderCallback: (initialProps: Props) => Result,
  options?: Omit<RenderHookOptions<Props>, 'wrapper'>,
) => rtlRenderHook(renderCallback, { wrapper: AllTheProviders, ...options })

export * from '@testing-library/react'
export { customRender as render, customRenderHook as renderHook }
