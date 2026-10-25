package app.albatross.vpn;

import android.app.Application;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.net.ConnectivityManager;
import android.net.LinkProperties;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.NetworkRequest;
import android.os.Build;

import java.net.Inet4Address;
import java.net.InetAddress;

import app.albatross.mobile.Mobile;

/** Starts the Go engine once per process and keeps it told about the network's resolver. */
public final class App extends Application {
    static final String CHANNEL = "status";
    // Used when the network does not say which resolver it has.
    private static final String FALLBACK_DNS = "8.8.8.8";

    private String uiUrl;

    @Override
    public void onCreate() {
        super.onCreate();
        if (Build.VERSION.SDK_INT >= 26) {
            NotificationChannel ch = new NotificationChannel(CHANNEL, getString(R.string.channel_name), NotificationManager.IMPORTANCE_LOW);
            ch.setShowBadge(false);
            getSystemService(NotificationManager.class).createNotificationChannel(ch);
        }
        // This app is left out of its own VPN, so its default network is the
        // real one (Wi-Fi or mobile data) even while connected.
        ConnectivityManager cm = getSystemService(ConnectivityManager.class);
        if (Build.VERSION.SDK_INT >= 24) {
            cm.registerDefaultNetworkCallback(new ConnectivityManager.NetworkCallback() {
                @Override
                public void onLinkPropertiesChanged(Network network, LinkProperties lp) {
                    String dns = pickDns(lp);
                    if (dns != null) Mobile.setDNS(dns);
                }
            });
            return;
        }
        // Android 6 reports every network with internet, not only the
        // default one: read the default one's resolver on each change.
        NetworkRequest req = new NetworkRequest.Builder().addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET).build();
        cm.registerNetworkCallback(req, new ConnectivityManager.NetworkCallback() {
            @Override
            public void onAvailable(Network network) {
                update();
            }

            @Override
            public void onLost(Network network) {
                update();
            }

            @Override
            public void onLinkPropertiesChanged(Network network, LinkProperties lp) {
                update();
            }

            private void update() {
                Network n = cm.getActiveNetwork();
                String dns = n == null ? null : pickDns(cm.getLinkProperties(n));
                if (dns != null) Mobile.setDNS(dns);
            }
        });
    }

    /** Starts the engine if needed and returns the UI address. Blocks; call off the main thread. */
    synchronized String engine() throws Exception {
        if (uiUrl == null) {
            uiUrl = Mobile.start(getFilesDir().getAbsolutePath(), getApplicationInfo().nativeLibraryDir, currentDns(), new Bridge(this));
        }
        return uiUrl;
    }

    synchronized void stopped() {
        uiUrl = null;
    }

    private String currentDns() {
        ConnectivityManager cm = getSystemService(ConnectivityManager.class);
        Network n = cm.getActiveNetwork();
        String dns = n == null ? null : pickDns(cm.getLinkProperties(n));
        return dns != null ? dns : FALLBACK_DNS;
    }

    // The network's own resolver, IPv4 first. Some mobile networks list
    // only IPv6 ones; a foreign fallback would answer Iranian sites with
    // their foreign addresses.
    private static String pickDns(LinkProperties lp) {
        if (lp == null) return null;
        String v6 = null;
        for (InetAddress a : lp.getDnsServers()) {
            if (a instanceof Inet4Address) return a.getHostAddress();
            if (v6 == null && !a.isLinkLocalAddress()) v6 = a.getHostAddress();
        }
        return v6;
    }
}
