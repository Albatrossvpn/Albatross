package app.albatross.vpn;

import android.Manifest;
import android.app.Activity;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.graphics.Color;
import android.net.Uri;
import android.net.VpnService;
import android.os.Build;
import android.os.Bundle;
import android.os.PowerManager;
import android.provider.Settings;
import android.view.View;
import android.view.WindowInsets;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.FrameLayout;
import android.widget.Toast;

import java.lang.ref.WeakReference;

import app.albatross.mobile.Mobile;

/** Shows the engine's web UI, the same page the Windows app shows. */
public final class MainActivity extends Activity {
    private static final int REQ_VPN = 1;
    private static WeakReference<MainActivity> visible = new WeakReference<>(null);

    private WebView web;
    private String origin;

    static MainActivity visible() {
        return visible.get();
    }

    /** Opens links away from the app's own page in the browser. */
    private boolean openOutside(Uri u) {
        if (origin != null && u.toString().startsWith(origin)) return false;
        try {
            startActivity(new Intent(Intent.ACTION_VIEW, u));
        } catch (Exception ignored) {
        }
        return true;
    }

    /** The Chrome version in a WebView's user agent, 0 when there is none. */
    static int chromeMajor(String ua) {
        int i = ua == null ? -1 : ua.indexOf("Chrome/");
        if (i < 0) return 0;
        int n = 0;
        for (i += "Chrome/".length(); i < ua.length() && Character.isDigit(ua.charAt(i)); i++) {
            n = n * 10 + (ua.charAt(i) - '0');
        }
        return n;
    }

    @Override
    protected void onCreate(Bundle saved) {
        super.onCreate(saved);
        FrameLayout root = new FrameLayout(this);
        root.setBackgroundColor(Color.parseColor("#0A0F1D"));
        web = new WebView(this);
        web.setBackgroundColor(Color.TRANSPARENT);
        WebSettings ws = web.getSettings();
        ws.setJavaScriptEnabled(true);
        ws.setDomStorageEnabled(true);
        ws.setSupportZoom(false);
        // WebViews before Chrome 80 (Android 10 ships 74) abort the whole
        // app when the GPU draws color emoji such as the flags.
        if (chromeMajor(ws.getUserAgentString()) < 80) web.setLayerType(View.LAYER_TYPE_SOFTWARE, null);
        web.setWebViewClient(new WebViewClient() {
            @Override
            public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest req) {
                return openOutside(req.getUrl());
            }

            // Android 6 calls only this one.
            @Override
            @SuppressWarnings("deprecation")
            public boolean shouldOverrideUrlLoading(WebView view, String url) {
                return openOutside(Uri.parse(url));
            }
        });
        root.addView(web, new FrameLayout.LayoutParams(FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));
        // Keep the page clear of the status and navigation bars (Android 15
        // draws apps edge to edge).
        root.setOnApplyWindowInsetsListener((v, insets) -> {
            int l, t, r, b;
            if (Build.VERSION.SDK_INT >= 30) {
                android.graphics.Insets i = insets.getInsets(WindowInsets.Type.systemBars() | WindowInsets.Type.ime());
                l = i.left; t = i.top; r = i.right; b = i.bottom;
            } else {
                l = insets.getSystemWindowInsetLeft(); t = insets.getSystemWindowInsetTop();
                r = insets.getSystemWindowInsetRight(); b = insets.getSystemWindowInsetBottom();
            }
            v.setPadding(l, t, r, b);
            return insets;
        });
        setContentView(root);

        App app = (App) getApplication();
        new Thread(() -> {
            try {
                String url = app.engine();
                runOnUiThread(() -> {
                    origin = url.substring(0, url.indexOf('/', "http://".length()));
                    web.loadUrl(url);
                });
            } catch (Exception e) {
                runOnUiThread(() -> {
                    Toast.makeText(this, getString(R.string.start_failed) + ": " + e.getMessage(), Toast.LENGTH_LONG).show();
                    finish();
                });
            }
        }).start();

        SelfTest.maybeRun(this, getIntent());
        if (saved == null) {
            // Ask now rather than in the middle of the first connection.
            askForVpn();
            if (Build.VERSION.SDK_INT >= 33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
                requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, 2);
            }
        }
    }

    /** Asks the user to allow the VPN if they have not yet; starts it if the engine wants it. */
    void askForVpn() {
        Intent i = VpnService.prepare(this);
        if (i != null) {
            startActivityForResult(i, REQ_VPN);
        }
    }

    @Override
    protected void onActivityResult(int req, int result, Intent data) {
        super.onActivityResult(req, result, data);
        if (req != REQ_VPN) return;
        boolean waiting = Bridge.takePending();
        if (result == RESULT_OK) {
            if (waiting) TunnelService.start(this);
        } else if (waiting) {
            // Without the permission the connection cannot carry traffic.
            Toast.makeText(this, R.string.vpn_denied, Toast.LENGTH_LONG).show();
            new Thread(Mobile::disconnect).start();
        }
    }

    @Override
    protected void onResume() {
        super.onResume();
        visible = new WeakReference<>(this);
        web.onResume();
        web.resumeTimers();
        askBatteryOnce();
    }

    /**
     * Once the VPN is allowed, asks once to leave the app out of battery
     * optimization: some phones otherwise kill the VPN while the screen is
     * off, and the connection drops until the app is opened again.
     */
    private void askBatteryOnce() {
        if (VpnService.prepare(this) != null) return;
        PowerManager pm = getSystemService(PowerManager.class);
        if (pm == null || pm.isIgnoringBatteryOptimizations(getPackageName())) return;
        android.content.SharedPreferences prefs = getSharedPreferences("app", MODE_PRIVATE);
        if (prefs.getBoolean("asked_battery", false)) return;
        prefs.edit().putBoolean("asked_battery", true).apply();
        try {
            startActivity(new Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:" + getPackageName())));
        } catch (Exception ignored) {
        }
    }

    @Override
    protected void onPause() {
        // A page nobody sees need not poll the engine every second.
        web.onPause();
        web.pauseTimers();
        if (visible.get() == this) visible = new WeakReference<>(null);
        super.onPause();
    }

    @Override
    @SuppressWarnings("deprecation")
    public void onBackPressed() {
        // The page closes its own sheets and returns to the home tab first.
        web.evaluateJavascript("window.albatrossBack ? albatrossBack() : false", handled -> {
            if (!"true".equals(handled)) moveTaskToBack(true);
        });
    }

    @Override
    protected void onDestroy() {
        web.destroy();
        super.onDestroy();
    }
}
