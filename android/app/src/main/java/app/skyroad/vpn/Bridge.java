package app.skyroad.vpn;

import android.content.Intent;
import android.net.Uri;
import android.net.VpnService;
import android.os.Handler;
import android.os.Looper;
import android.widget.Toast;

import app.skyroad.mobile.Mobile;
import app.skyroad.mobile.Platform;

/** What the Go engine asks of Android. Calls arrive on Go's threads. */
final class Bridge implements Platform {
    private final App app;
    private final Handler main = new Handler(Looper.getMainLooper());
    // The engine wanted the VPN while the user had not allowed it yet.
    private static boolean pending;

    /** Reports and clears whether a VPN start is waiting for the user's permission. Main thread only. */
    static boolean takePending() {
        boolean p = pending;
        pending = false;
        return p;
    }

    Bridge(App app) {
        this.app = app;
    }

    @Override
    public void startVPN() {
        main.post(() -> {
            if (VpnService.prepare(app) == null) {
                TunnelService.start(app);
                return;
            }
            pending = true;
            // Android asks the user once before any app may run a VPN, and
            // only an open screen can ask.
            MainActivity a = MainActivity.visible();
            if (a != null) {
                a.askForVpn();
            } else {
                pending = false;
                Toast.makeText(app, R.string.vpn_denied, Toast.LENGTH_LONG).show();
                new Thread(Mobile::disconnect).start();
            }
        });
    }

    @Override
    public void stopVPN() {
        main.post(() -> TunnelService.stop(app));
    }

    @Override
    public void openURL(String url) {
        main.post(() -> {
            try {
                app.startActivity(new Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK));
            } catch (Exception ignored) {
            }
        });
    }

    @Override
    public void quit() {
        main.post(() -> {
            TunnelService.stop(app);
            MainActivity a = MainActivity.visible();
            if (a != null) a.finishAndRemoveTask();
            new Thread(() -> {
                Mobile.stop();
                app.stopped();
                System.exit(0);
            }).start();
        });
    }
}
