package app.albatross.vpn;

import android.app.AppOpsManager;
import android.content.Intent;
import android.net.Uri;
import android.net.VpnService;
import android.os.Handler;
import android.os.Looper;
import android.provider.Settings;
import android.widget.Toast;

import java.net.Inet4Address;
import java.net.InetAddress;
import java.net.NetworkInterface;
import java.util.Collections;

import app.albatross.mobile.Mobile;
import app.albatross.mobile.Platform;

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
        // Runs off the main thread (App.engine), before the engine can make
        // a new copy.
        ApkProvider.removeSpentCopy(app);
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
    public void shareText(String text) {
        main.post(() -> {
            Intent send = new Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, text);
            share(send);
        });
    }

    @Override
    public void shareApp() {
        main.post(() -> {
            Intent send = new Intent(Intent.ACTION_SEND)
                    .setType("application/vnd.android.package-archive")
                    .putExtra(Intent.EXTRA_STREAM, ApkProvider.URI)
                    .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
            share(send);
        });
    }

    // Bluetooth and Quick Share appear in the share sheet and work without
    // any internet.
    private void share(Intent send) {
        try {
            app.startActivity(Intent.createChooser(send, null).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_GRANT_READ_URI_PERMISSION));
        } catch (Exception ignored) {
        }
    }

    @Override
    public void installUpdate(String path) throws Exception {
        java.io.File f = new java.io.File(path);
        if (!f.canRead()) throw new java.io.FileNotFoundException(path);
        // Kept on disk, not only in memory: allowing "Install unknown apps"
        // restarts the app's process before the installer reads the file.
        ApkProvider.setUpdate(app, f);
        // A failure shows on the update banner, worded by the engine; a toast
        // would come again with each background try of a forced update.
        app.startActivity(new Intent(Intent.ACTION_VIEW)
                .setDataAndType(ApkProvider.updateUri(app, f), ApkProvider.TYPE)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_GRANT_READ_URI_PERMISSION));
    }

    // Go cannot list the network interfaces on Android 11 and later; Java
    // can. Cellular and tunnel interfaces are left out: other devices reach
    // this phone only on its hotspot, Wi-Fi or USB/Bluetooth tethering.
    @Override
    public String lanAddrs() {
        StringBuilder out = new StringBuilder();
        try {
            for (NetworkInterface ni : Collections.list(NetworkInterface.getNetworkInterfaces())) {
                String name = ni.getName();
                if (!ni.isUp() || ni.isLoopback() || name.startsWith("rmnet") || name.startsWith("ccmni")
                        || name.startsWith("tun") || name.startsWith("dummy") || name.startsWith("clat")
                        || name.startsWith("v4-") || name.startsWith("ppp")) continue;
                for (InetAddress a : Collections.list(ni.getInetAddresses())) {
                    if (!(a instanceof Inet4Address) || !a.isSiteLocalAddress()) continue;
                    if (out.length() > 0) out.append(',');
                    out.append(a.getHostAddress());
                }
            }
        } catch (Exception ignored) {
        }
        return out.toString();
    }

    @Override
    public void openVPNSettings() {
        main.post(() -> {
            try {
                app.startActivity(new Intent(Settings.ACTION_VPN_SETTINGS).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK));
            } catch (Exception ignored) {
            }
        });
    }

    // "Override GPS location" needs Developer options on and Albatross chosen
    // there as the mock location app; the page walks the user through both.
    @Override
    public String locationSetup() {
        try {
            if (Settings.Global.getInt(app.getContentResolver(), Settings.Global.DEVELOPMENT_SETTINGS_ENABLED, 0) != 1) {
                return "developer";
            }
            AppOpsManager ops = app.getSystemService(AppOpsManager.class);
            if (ops.checkOpNoThrow(AppOpsManager.OPSTR_MOCK_LOCATION, android.os.Process.myUid(), app.getPackageName()) == AppOpsManager.MODE_ALLOWED) {
                return "";
            }
        } catch (Exception ignored) {
        }
        return "mock_app";
    }

    @Override
    public void openLocationSettings(String step) {
        main.post(() -> {
            // About phone, where tapping Build number turns Developer options
            // on; or Developer options, where the mock location app is chosen.
            String action = "developer".equals(step) ? Settings.ACTION_DEVICE_INFO_SETTINGS : Settings.ACTION_APPLICATION_DEVELOPMENT_SETTINGS;
            try {
                app.startActivity(new Intent(action).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK));
            } catch (Exception e) {
                try {
                    app.startActivity(new Intent(Settings.ACTION_SETTINGS).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK));
                } catch (Exception ignored) {
                }
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
