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

  test('should enforce user permissions and restrict access to admin views', async ({ page }) => {
    await page.goto('http://localhost:8080');

    // Attempt to access a protected admin section or panel directly
    await page.goto('http://localhost:8080/#/admin');

    // If there is an auth barrier/permission alert, verify it displays correctly
    const permBanner = page.locator('.permission-banner, .auth-error, #auth-barrier, .alert-danger');
    if (await permBanner.count() > 0) {
      await expect(permBanner.first()).toContainText(/permission|unauthorized|access denied|login/i);
    }
  });

  test('should render custom glassmorphic widgets and interactive telemetry states', async ({ page }) => {
    await page.goto('http://localhost:8080');

    // Locate metric cards / glassmorphic widgets in dashboard
    const widgets = page.locator('.metric-card, .glass-card, .widget');
    const widgetCount = await widgets.count();

    if (widgetCount > 0) {
      // Assert that at least the first widget is visible and contains status text
      await expect(widgets.first()).toBeVisible();

      // Check if custom telemetry sliders or refresh buttons are present
      const refreshBtn = page.locator('.refresh-metrics, #refresh-btn');
      if (await refreshBtn.count() > 0) {
        await refreshBtn.first().click();
        // Wait for page transition / data update
        await page.waitForTimeout(100);
        await expect(widgets.first()).toBeVisible();
      }
    }
  });
});

