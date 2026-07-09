const { test, expect } = require('@playwright/test');

test.describe('ServConsole Dashboard E2E Tests', () => {
  test('should load landing page and display main dashboard layout elements', async ({ page }) => {
    // Navigate to local console URL (mocking/simulating standard page server)
    await page.goto('http://localhost:8080');

    // Verify main app title or branding container exists
    const container = page.locator('#app');
    await expect(container).toBeVisible();

    // Check presence of basic header/navbar elements
    const navbar = page.locator('nav, header, .navbar');
    if (await navbar.count() > 0) {
      await expect(navbar.first()).toBeVisible();
    }
  });

  test('should support route changes or page view transitions', async ({ page }) => {
    await page.goto('http://localhost:8080');

    // Attempt to click on tabs/links if available
    const navLinks = page.locator('a, button');
    const count = await navLinks.count();
    if (count > 0) {
      await navLinks.first().click();
      // Ensure page did not crash and main container is still active
      await expect(page.locator('#app')).toBeVisible();
    }
  });
});
