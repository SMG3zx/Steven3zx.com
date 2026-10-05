// Java 25 source launcher. Working directory: target/configuration-source.
import java.nio.file.*;
import java.net.*;
import java.util.*;
import javax.tools.ToolProvider;
import java.security.MessageDigest;
import java.util.zip.ZipFile;

class RunWorldExtractor {
    public static void main(String[] args) throws Exception {
        String serverHash = HexFormat.of().formatHex(MessageDigest.getInstance("SHA-1").digest(Files.readAllBytes(Path.of("server.jar"))));
        if (!serverHash.equals("33680f5f2ac32864d6d7cf5e56a705fdb3e05f4c")) throw new IllegalStateException("Wrong official server SHA-1");
        var jars = new ArrayList<Path>();
        try (var zip = new ZipFile("server.jar")) {
            for (String directory : List.of("versions", "libraries")) {
                String manifest = new String(zip.getInputStream(zip.getEntry("META-INF/" + directory + ".list")).readAllBytes(), java.nio.charset.StandardCharsets.UTF_8);
                for (String line : manifest.lines().toList()) {
                    String[] fields = line.split("\\t");
                    Path jar = Path.of(directory, fields[2]);
                    String hash = HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(Files.readAllBytes(jar)));
                    if (!hash.equals(fields[0])) throw new IllegalStateException("Bundled JAR hash mismatch: " + jar);
                    jars.add(jar);
                }
            }
        }
        var classpath = String.join(java.io.File.pathSeparator, jars.stream().map(p -> p.toAbsolutePath().toString()).toList());
        Path classes = Path.of("extractor-classes"); Files.createDirectories(classes);
        int result = ToolProvider.getSystemJavaCompiler().run(null, null, null, "-classpath", classpath, "-d", classes.toString(), "../../tools/ExtractWorldInitialization.java");
        if (result != 0) throw new IllegalStateException("javac failed: " + result);
        var urls = new ArrayList<URL>(); urls.add(classes.toUri().toURL());
        for (Path jar : jars) urls.add(jar.toUri().toURL());
        try (var loader = new URLClassLoader(urls.toArray(URL[]::new), ClassLoader.getPlatformClassLoader())) {
            Thread.currentThread().setContextClassLoader(loader);
            loader.loadClass("ExtractWorldInitialization").getMethod("main", String[].class).invoke(null, (Object) args);
        }
    }
}
