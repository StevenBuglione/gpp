package port;

public class GoBackedBase {
    private final long handle;

    protected GoBackedBase(long handle) {
        this.handle = handle;
    }

    private static native int readValue(long handle);

    public int value() {
        return readValue(handle);
    }

    public final GoBackedBase identity() {
        return this;
    }
}
