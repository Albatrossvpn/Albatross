package app.albatross.vpn;

import android.content.ContentProvider;
import android.content.ContentValues;
import android.content.Context;
import android.database.Cursor;
import android.database.MatrixCursor;
import android.net.Uri;
import android.os.ParcelFileDescriptor;
import android.provider.OpenableColumns;

import java.io.File;
import java.io.FileNotFoundException;

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
