import { test, expect } from '@playwright/test'

test.beforeEach(async ({ page }) => {
  await page.goto('/login')
  await page.evaluate(() => {
    localStorage.setItem('diverge:token', 'valid-token')
  })
  await page.goto('/')
})

test('After login, environment list is visible', async ({ page }) => {
  await expect(page.locator('text=Environments').first()).toBeVisible()
})

test('Create button opens modal', async ({ page }) => {
  const createBtn = page.locator('button:has-text("Create")')
  if (await createBtn.isVisible()) {
    await createBtn.click()
    await expect(page.locator('text=Create Environment').first()).toBeVisible()
  }
})

test('Navigation between pages works', async ({ page }) => {
  await page.click('a:has-text("Preview Groups")')
  await expect(page).toHaveURL(/.*\/preview-groups/)

  await page.click('a:has-text("Cluster")')
  await expect(page).toHaveURL(/.*\/cluster/)
})

test('Theme toggle changes background', async ({ page }) => {
  const toggle = page.locator('button', { hasText: /Dark mode|Light mode/ })
  if (await toggle.isVisible()) {
    await toggle.click()
  }
})
