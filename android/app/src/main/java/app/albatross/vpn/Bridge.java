package app.albatross.vpn;

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
    public void installUpdate(String path) {
        main.post(() -> {
            ApkProvider.update = new java.io.File(path);
            try {
                app.startActivity(new Intent(Intent.ACTION_VIEW)
                        .setDataAndType(ApkProvider.UPDATE_URI, ApkProvider.TYPE)
                        .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_GRANT_READ_URI_PERMISSION));
            } catch (Exception e) {
                Toast.makeText(app, R.string.update_failed, Toast.LENGTH_LONG).show();
            }
        });
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
