{
  lib,
  buildGoModule,
}:
buildGoModule (finalAttrs: {
  pname = "esb";
  version = "0.1.0";

  src = lib.cleanSource ../.;

  vendorHash = "sha256-U7NhFwD3K0Qnq+x/2XuyQnhJo+wXqJggmzg4nB6Ok6U=";

  ldflags = [
    "-s"
    "-w"
    "-X=github.com/rhawk/esb/cmd.version=${finalAttrs.version}"
  ];

  # The Caddyfile adapter test is what proves the deSEC plugin is still wired
  # up, so it is worth paying for on every build.
  doCheck = true;

  # Deliberately not wrapped with a PATH: esb shells out to `sbx`, `docker`,
  # and `git`, all of which come from Homebrew or Docker Desktop on this
  # machine. Pinning them to nixpkgs copies would find the wrong binaries.

  meta = {
    description = "Name-based HTTPS routing for Docker Sandbox microVMs";
    longDescription = ''
      esb (extended sandbox) gives every Docker Sandbox a real hostname behind
      a publicly trusted wildcard certificate. It embeds Caddy with the deSEC
      DNS-01 provider and a minimal authoritative DNS server, so there is no
      separate Caddy build and no dnsmasq to install.
    '';
    homepage = "https://github.com/rhawk/esb";
    license = lib.licenses.mit;
    mainProgram = "esb";
    platforms = lib.platforms.unix;
  };
})
