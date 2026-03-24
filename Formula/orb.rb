class Orb < Formula
  desc "CLI-native agentic coding interface for Codex and Claude"
  homepage "https://github.com/willsantiagomedina/orb"
  head "https://github.com/willsantiagomedina/orb.git", branch: "main"

  depends_on "go" => :build

  def install
    odie "Orb Homebrew install currently supports macOS only." unless OS.mac?
    system "go", "build", *std_go_args(ldflags: "-s -w"), "./cmd/orb"
  end

  test do
    assert_match "orb ", shell_output("#{bin}/orb -version")
  end
end
