package app.albatross.vpn;

import android.content.ContentProvider;
import android.content.ContentValues;
import android.content.Context;
import android.content.pm.PackageManager;
import android.database.Cursor;
import android.database.MatrixCursor;
import android.net.Uri;
import android.os.Build;
import android.os.ParcelFileDescriptor;
import android.provider.OpenableColumns;

import java.io.File;
import java.io.FileInputStream;
import java.io.FileNotFoundException;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;

/**
 * Serves the app's own installer to the app that the user shares it with,
 * and a downloaded newer version to the system installer.
 */
public final class ApkProvider extends ContentProvider {
    static final String AUTHORITY = "app.albatross.vpn.apk";
    static final String NAME = "Albatross.apk";
    static final Uri URI = Uri.parse("content://" + AUTHORITY + "/" + NAME);
    static final String UPDATE_NAME = "update.apk";
    static final Uri UPDATE_URI = Uri.parse("content://" + AUTHORITY + "/" + UPDATE_NAME);
    static final String TYPE = "application/vnd.android.package-archive";
    private static final String PREFS = "update";

    // The downloaded new version (Bridge.installUpdate), remembered across
    // process restarts.
    static void setUpdate(Context ctx, File f) {
        ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putString("path", f.getAbsolutePath()).commit();
    }

    // Where the system installer reads the update from. Android 6's opens
    // only file:// links, so it gets a copy in the app's external cache,
    // which the app may write without a permission and the installer, which
    // holds the storage one, may read. Android 7 and later read the update
    // through this provider.
    static Uri updateUri(Context ctx, File f) throws IOException {
        if (Build.VERSION.SDK_INT >= 24) return UPDATE_URI;
        File dir = ctx.getExternalCacheDir();
        if (dir == null) throw new FileNotFoundException("no shared storage");
        File copy = new File(dir, UPDATE_NAME);
        // Half a copy would stay, and a forced update's next try would fill
        // the phone's storage again: check for room first, and never leave
        // one behind.
        if (dir.getUsableSpace() + copy.length() < f.length()) {
            copy.delete();
            throw new IOException("no room for the update's copy");
        }
        try (InputStream in = new FileInputStream(f); OutputStream out = new FileOutputStream(copy)) {
            byte[] buf = new byte[64 << 10];
            for (int n; (n = in.read(buf)) > 0; ) out.write(buf, 0, n);
        } catch (IOException e) {
            copy.delete();
            throw e;
        }
        return Uri.fromFile(copy);
    }

    // Deletes Android 6's copy once the app has been installed since it was
    // made: it was that update or one given up, and each try makes its own.
    static void removeSpentCopy(Context ctx) {
        if (Build.VERSION.SDK_INT >= 24) return;
        File dir = ctx.getExternalCacheDir();
        if (dir == null) return;
        File copy = new File(dir, UPDATE_NAME);
        try {
            long installed = ctx.getPackageManager().getPackageInfo(ctx.getPackageName(), 0).lastUpdateTime;
            if (copy.lastModified() < installed) copy.delete();
        } catch (PackageManager.NameNotFoundException ignored) {
        }
    }

    private File apk(Uri uri) throws FileNotFoundException {
        String name = uri.getLastPathSegment();
        if (NAME.equals(name)) return new File(getContext().getApplicationInfo().sourceDir);
        if (UPDATE_NAME.equals(name)) {
            String path = getContext().getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString("path", null);
            if (path != null && new File(path).canRead()) return new File(path);
        }
        throw new FileNotFoundException();
    }

    @Override
    public boolean onCreate() {
        return true;
    }

    @Override
    public String getType(Uri uri) {
        return TYPE;
    }

    @Override
    public ParcelFileDescriptor openFile(Uri uri, String mode) throws FileNotFoundException {
        if (!"r".equals(mode)) throw new FileNotFoundException();
        return ParcelFileDescriptor.open(apk(uri), ParcelFileDescriptor.MODE_READ_ONLY);
    }

    @Override
    public Cursor query(Uri uri, String[] projection, String selection, String[] args, String sort) {
        MatrixCursor c = new MatrixCursor(new String[] {OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE});
        try {
            c.addRow(new Object[] {uri.getLastPathSegment(), apk(uri).length()});
        } catch (FileNotFoundException ignored) {
        }
        return c;
    }

    @Override
    public Uri insert(Uri uri, ContentValues values) {
        return null;
    }

    @Override
    public int delete(Uri uri, String selection, String[] args) {
        return 0;
    }

    @Override
    public int update(Uri uri, ContentValues values, String selection, String[] args) {
        return 0;
    }
}
