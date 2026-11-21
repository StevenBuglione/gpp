import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.nio.charset.StandardCharsets;
import java.util.Locale;

class JavaPrimitiveProbe {
    public static void main(String[] args) throws Exception {
        try (var input = new BufferedReader(new InputStreamReader(System.in, StandardCharsets.UTF_8))) {
            String line;
            while ((line = input.readLine()) != null) {
                long bits = Long.parseUnsignedLong(line, 16);
                double value = Double.longBitsToDouble(bits);
                System.out.printf(Locale.ROOT, "%016x %d %d %s%n",
                    bits, (int) value, (long) value, Double.toString(value));
            }
        }
    }
}
