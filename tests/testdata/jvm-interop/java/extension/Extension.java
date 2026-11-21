package extension;

import port.GoBackedBase;

public class Extension extends GoBackedBase {
    public Extension(long handle) {
        super(handle);
    }

    @Override
    public int value() {
        return super.value() + 2;
    }
}
