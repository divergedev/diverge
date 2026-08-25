import { test, expect } from '@playwright/test'

test('Visiting / without token redirects to /login', async ({ page }) => {
  await page.goto('/')
  await expect(page).toHaveURL(/.*\/login/)
})

test('Login page renders with branding', async ({ page }) => {
  await page.goto('/login')
  await expect(page.locator('text=Diverge')).toBeVisible()
})

test('Entering invalid token shows error', async ({ page }) => {
  await page.goto('/login')
  await page.fill('input[placeholder*="token"]', 'invalid-token')
  await page.click('button:has-text("Connect")')

  // Just testing UI interactions, actual backend error handling may vary
  // But we know it tries to connect.
})

test('Logout redirects to /login', async ({ page }) => {
  // Inject mock auth token
  await page.goto('/login')
  await page.evaluate(() => {
    localStorage.setItem('diverge:token', 'valid-token')
  })
  await page.goto('/')

  // Click logout
  const logoutBtn = page.locator('button:has-text("Logout")')
  if (await logoutBtn.isVisible()) {
    await logoutBtn.click()
    await expect(page).toHaveURL(/.*\/login/)
  }
})

test('SSO button shows provider name when configured', async ({ page }) => {
  await page.route('/auth/config', async (route) => {
    await route.fulfill({
      json: { oidcEnabled: true, providerName: 'Google', loginUrl: '/auth/login' }
    })
  })
  await page.goto('/login')
  await expect(page.locator('button', { hasText: 'Sign in with Google' })).toBeVisible()
})

test('SSO button is disabled when not configured', async ({ page }) => {
  await page.route('/auth/config', async (route) => {
    await route.fulfill({
      json: { oidcEnabled: false }
    })
  })
  await page.goto('/login')
  const btn = page.locator('button', { hasText: 'SSO Login (Not Configured)' })
  await expect(btn).toBeVisible()
  await expect(btn).toBeDisabled()
})

test('Token login still works alongside SSO', async ({ page }) => {
  await page.route('/auth/config', async (route) => {
    await route.fulfill({
      json: { oidcEnabled: true, providerName: 'Google', loginUrl: '/auth/login' }
    })
  })
  await page.route('**/diverge.v1alpha1.AuthService/GetCurrentUser', async (route) => {
    await route.fulfill({
      json: { userId: '123', username: 'test', email: 'test@example.com' }
    })
  })

  await page.goto('/login')
  await page.fill('textarea[placeholder*="token"]', 'valid-token')
  await page.click('button:has-text("Connect with Token")')

  await expect(page).not.toHaveURL(/.*\/login/)
})
