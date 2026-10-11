// Project icon emoji support (C18T4).
//
// EMOJI_LIST is the curated grid for the project icon picker. Every entry
// is a single emoji per the server contract (internal/service/project.go:
// isSingleEmoji) — the picker test asserts this over the whole list, so a
// bad entry fails the build, not the user.
//
// isSingleEmoji mirrors the server validator for instant client-side
// feedback. The server is the authority: anything this misses is still
// rejected with a 400.

export interface EmojiEntry {
  emoji: string;
  name: string;
  keywords: string[];
}

const ZWJ = 0x200d;
const TEXT_PRESENTATION = 0xfe0e;
const VS16 = 0xfe0f;
const ENCLOSING_KEYCAP = 0x20e3;

function isEmojiBase(cp: number): boolean {
  return (
    (cp >= 0x1f300 && cp <= 0x1faff) ||
    (cp >= 0x2600 && cp <= 0x27bf) ||
    (cp >= 0x2b00 && cp <= 0x2bff) ||
    (cp >= 0x2300 && cp <= 0x23ff) ||
    cp === 0x00a9 ||
    cp === 0x00ae
  );
}

function isRegionalIndicator(cp: number): boolean {
  return cp >= 0x1f1e6 && cp <= 0x1f1ff;
}

function isSkinToneModifier(cp: number): boolean {
  return cp >= 0x1f3fb && cp <= 0x1f3ff;
}

function isKeycapBase(cp: number): boolean {
  return (
    (cp >= 0x30 && cp <= 0x39) || cp === 0x23 || cp === 0x2a // 0-9, #, *
  );
}

/** True when s is exactly one emoji: a base, base+VS16/skin-tone, a flag
 *  pair, or a keycap. Rejects ZWJ sequences and multi-emoji strings. */
export function isSingleEmoji(s: string): boolean {
  const cps = Array.from(s).map((ch) => ch.codePointAt(0) ?? 0);
  for (const cp of cps) {
    if (cp === ZWJ || cp === TEXT_PRESENTATION) return false;
    if (cp <= 0x1f || cp === 0x7f || cp === 0x20) return false;
  }
  switch (cps.length) {
    case 1:
      return isEmojiBase(cps[0]);
    case 2:
      if (isRegionalIndicator(cps[0]) && isRegionalIndicator(cps[1]))
        return true; // flag
      // Bare keycap (no VS16), e.g. 1⃣, is still one emoji.
      if (isKeycapBase(cps[0]) && cps[1] === ENCLOSING_KEYCAP) return true;
      return (
        isEmojiBase(cps[0]) &&
        (cps[1] === VS16 || isSkinToneModifier(cps[1]))
      );
    case 3:
      return (
        isKeycapBase(cps[0]) &&
        cps[1] === VS16 &&
        cps[2] === ENCLOSING_KEYCAP
      );
    default:
      return false;
  }
}

export const EMOJI_LIST: EmojiEntry[] = [
  // Faces
  { emoji: "😀", name: "grinning", keywords: ["smile", "happy", "face"] },
  { emoji: "😃", name: "smiley", keywords: ["smile", "happy", "face"] },
  { emoji: "😄", name: "smile", keywords: ["smile", "happy", "face"] },
  { emoji: "😁", name: "beam", keywords: ["smile", "happy", "grin", "face"] },
  { emoji: "😆", name: "laugh", keywords: ["smile", "happy", "laugh", "face"] },
  { emoji: "😅", name: "sweat smile", keywords: ["smile", "sweat", "face"] },
  { emoji: "🤣", name: "rofl", keywords: ["laugh", "lol", "face"] },
  { emoji: "😂", name: "joy", keywords: ["laugh", "cry", "tears", "face"] },
  { emoji: "🙂", name: "slight smile", keywords: ["smile", "face"] },
  { emoji: "🙃", name: "upside down", keywords: ["silly", "face"] },
  { emoji: "😉", name: "wink", keywords: ["face", "flirt"] },
  { emoji: "😊", name: "blush", keywords: ["smile", "shy", "face"] },
  { emoji: "😇", name: "halo", keywords: ["angel", "innocent", "face"] },
  { emoji: "🥰", name: "hearts", keywords: ["love", "smile", "face"] },
  { emoji: "😍", name: "heart eyes", keywords: ["love", "face"] },
  { emoji: "🤩", name: "star struck", keywords: ["wow", "excited", "face"] },
  { emoji: "😘", name: "kiss", keywords: ["love", "face"] },
  { emoji: "😋", name: "yum", keywords: ["tasty", "tongue", "face"] },
  { emoji: "😛", name: "tongue", keywords: ["silly", "face"] },
  { emoji: "😜", name: "winking tongue", keywords: ["silly", "face"] },
  { emoji: "🤪", name: "zany", keywords: ["crazy", "silly", "face"] },
  { emoji: "🤑", name: "money face", keywords: ["rich", "dollar", "face"] },
  { emoji: "🤗", name: "hug", keywords: ["hug", "care", "face"] },
  { emoji: "🤔", name: "thinking", keywords: ["think", "hmm", "face"] },
  { emoji: "😐", name: "neutral", keywords: ["meh", "face"] },
  { emoji: "🙄", name: "eye roll", keywords: ["annoyed", "face"] },
  { emoji: "😬", name: "grimace", keywords: ["awkward", "face"] },
  { emoji: "😴", name: "sleep", keywords: ["tired", "zzz", "face"] },
  { emoji: "😷", name: "mask", keywords: ["sick", "face"] },
  { emoji: "🤒", name: "thermometer", keywords: ["sick", "fever", "face"] },
  { emoji: "🥵", name: "hot", keywords: ["heat", "sweat", "face"] },
  { emoji: "🥶", name: "cold", keywords: ["freeze", "ice", "face"] },
  { emoji: "😵", name: "dizzy", keywords: ["knocked", "out", "face"] },
  { emoji: "🤯", name: "exploding head", keywords: ["mind", "blown", "face"] },
  { emoji: "🤠", name: "cowboy", keywords: ["hat", "yeehaw", "face"] },
  { emoji: "🥳", name: "party", keywords: ["celebrate", "birthday", "face"] },
  { emoji: "😎", name: "sunglasses", keywords: ["cool", "face"] },
  { emoji: "🤓", name: "nerd", keywords: ["geek", "glasses", "face"] },
  { emoji: "😮", name: "open mouth", keywords: ["wow", "surprised", "face"] },
  { emoji: "😲", name: "astonished", keywords: ["shocked", "face"] },
  { emoji: "🥺", name: "pleading", keywords: ["puppy", "eyes", "please", "face"] },
  { emoji: "😢", name: "cry", keywords: ["tear", "sad", "face"] },
  { emoji: "😭", name: "sob", keywords: ["cry", "bawl", "face"] },
  { emoji: "😱", name: "scream", keywords: ["scared", "shock", "face"] },
  { emoji: "😤", name: "triumph", keywords: ["steam", "angry", "face"] },
  { emoji: "😡", name: "rage", keywords: ["angry", "mad", "face"] },
  { emoji: "😈", name: "devil", keywords: ["evil", "horns", "face"] },
  { emoji: "💀", name: "skull", keywords: ["dead", "death"] },
  // Gestures
  { emoji: "👋", name: "wave", keywords: ["hello", "hi", "hand"] },
  { emoji: "🤚", name: "raised back of hand", keywords: ["hand", "stop"] },
  { emoji: "✋", name: "raised hand", keywords: ["hand", "high", "five"] },
  { emoji: "👌", name: "ok hand", keywords: ["hand", "perfect", "ok"] },
  { emoji: "✌️", name: "victory", keywords: ["peace", "hand", "two"] },
  { emoji: "🤞", name: "crossed fingers", keywords: ["luck", "hope", "hand"] },
  { emoji: "🤏", name: "pinch", keywords: ["small", "tiny", "hand"] },
  { emoji: "👍", name: "thumbs up", keywords: ["yes", "like", "approve", "hand"] },
  { emoji: "👎", name: "thumbs down", keywords: ["no", "dislike", "hand"] },
  { emoji: "👏", name: "clap", keywords: ["applause", "bravo", "hands"] },
  { emoji: "🙌", name: "raising hands", keywords: ["hooray", "celebrate", "hands"] },
  { emoji: "👐", name: "open hands", keywords: ["hug", "hands"] },
  { emoji: "🤝", name: "handshake", keywords: ["deal", "agree", "hands"] },
  { emoji: "✊", name: "fist", keywords: ["power", "hand"] },
  { emoji: "👊", name: "oncoming fist", keywords: ["punch", "hand"] },
  { emoji: "💪", name: "flex", keywords: ["strong", "muscle", "arm"] },
  { emoji: "🫶", name: "heart hands", keywords: ["love", "hands"] },
  // Objects & work
  { emoji: "🚀", name: "rocket", keywords: ["launch", "ship", "fast", "space"] },
  { emoji: "🎯", name: "target", keywords: ["goal", "bullseye", "aim"] },
  { emoji: "💡", name: "bulb", keywords: ["idea", "light", "think"] },
  { emoji: "🔥", name: "fire", keywords: ["hot", "lit", "flame"] },
  { emoji: "⭐", name: "star", keywords: ["favorite", "rating"] },
  { emoji: "🌟", name: "glowing star", keywords: ["sparkle", "star"] },
  { emoji: "✨", name: "sparkles", keywords: ["shine", "new", "magic"] },
  { emoji: "💎", name: "gem", keywords: ["diamond", "precious"] },
  { emoji: "🏆", name: "trophy", keywords: ["win", "champion", "award"] },
  { emoji: "🎉", name: "party popper", keywords: ["celebrate", "tada"] },
  { emoji: "🎈", name: "balloon", keywords: ["party", "celebrate"] },
  { emoji: "🎁", name: "gift", keywords: ["present", "birthday"] },
  { emoji: "📣", name: "megaphone", keywords: ["announce", "loud", "shout"] },
  { emoji: "📌", name: "pin", keywords: ["pinned", "pushpin"] },
  { emoji: "📝", name: "memo", keywords: ["write", "note", "pencil"] },
  { emoji: "📚", name: "books", keywords: ["read", "study", "library"] },
  { emoji: "🔍", name: "magnifier", keywords: ["search", "find", "zoom"] },
  { emoji: "💻", name: "laptop", keywords: ["computer", "code"] },
  { emoji: "📱", name: "phone", keywords: ["mobile", "iphone"] },
  { emoji: "📷", name: "camera", keywords: ["photo", "picture"] },
  { emoji: "🎬", name: "clapper", keywords: ["movie", "film", "video"] },
  { emoji: "🎙️", name: "microphone", keywords: ["mic", "podcast", "record"] },
  { emoji: "🎧", name: "headphones", keywords: ["music", "audio", "listen"] },
  { emoji: "🎵", name: "music note", keywords: ["music", "song"] },
  { emoji: "🎸", name: "guitar", keywords: ["music", "rock"] },
  { emoji: "🎨", name: "palette", keywords: ["art", "paint", "design"] },
  { emoji: "📦", name: "package", keywords: ["box", "ship", "parcel"] },
  { emoji: "📊", name: "bar chart", keywords: ["stats", "graph", "data"] },
  { emoji: "📈", name: "chart up", keywords: ["growth", "trend", "up"] },
  { emoji: "⏰", name: "alarm", keywords: ["clock", "time", "wake"] },
  { emoji: "💰", name: "money bag", keywords: ["cash", "dollar", "rich"] },
  { emoji: "🔧", name: "wrench", keywords: ["tool", "fix", "repair"] },
  { emoji: "🔨", name: "hammer", keywords: ["tool", "build"] },
  { emoji: "⚙️", name: "gear", keywords: ["settings", "cog", "config"] },
  { emoji: "🧲", name: "magnet", keywords: ["attract", "metal"] },
  { emoji: "🧪", name: "test tube", keywords: ["science", "lab", "experiment"] },
  { emoji: "🧬", name: "dna", keywords: ["science", "gene", "biology"] },
  { emoji: "🔬", name: "microscope", keywords: ["science", "lab", "zoom"] },
  { emoji: "📡", name: "satellite", keywords: ["signal", "dish", "broadcast"] },
  { emoji: "✈️", name: "airplane", keywords: ["flight", "travel", "plane"] },
  { emoji: "🚗", name: "car", keywords: ["drive", "auto", "vehicle"] },
  { emoji: "🚲", name: "bicycle", keywords: ["bike", "cycle"] },
  { emoji: "🏍️", name: "motorcycle", keywords: ["bike", "moto"] },
  // Nature & animals
  { emoji: "🌵", name: "cactus", keywords: ["desert", "plant"] },
  { emoji: "🌲", name: "pine", keywords: ["tree", "forest"] },
  { emoji: "🌴", name: "palm", keywords: ["tree", "tropical", "beach"] },
  { emoji: "🌱", name: "seedling", keywords: ["plant", "grow", "new"] },
  { emoji: "🌷", name: "tulip", keywords: ["flower", "spring"] },
  { emoji: "🌹", name: "rose", keywords: ["flower", "love"] },
  { emoji: "🌻", name: "sunflower", keywords: ["flower", "sun"] },
  { emoji: "🌞", name: "sun", keywords: ["sunny", "weather", "day"] },
  { emoji: "🌈", name: "rainbow", keywords: ["weather", "pride", "colors"] },
  { emoji: "☁️", name: "cloud", keywords: ["weather", "sky"] },
  { emoji: "⛈️", name: "storm", keywords: ["thunder", "rain", "weather"] },
  { emoji: "❄️", name: "snowflake", keywords: ["snow", "winter", "cold"] },
  { emoji: "🌊", name: "wave", keywords: ["ocean", "sea", "surf"] },
  { emoji: "🐶", name: "dog", keywords: ["puppy", "pet"] },
  { emoji: "🐱", name: "cat", keywords: ["kitten", "pet"] },
  { emoji: "🐰", name: "rabbit", keywords: ["bunny", "pet"] },
  { emoji: "🦊", name: "fox", keywords: ["clever"] },
  { emoji: "🐻", name: "bear", keywords: ["teddy"] },
  { emoji: "🐼", name: "panda", keywords: ["bear"] },
  { emoji: "🦁", name: "lion", keywords: ["king", "brave"] },
  { emoji: "🐸", name: "frog", keywords: ["toad"] },
  { emoji: "🐵", name: "monkey", keywords: ["ape"] },
  { emoji: "🐧", name: "penguin", keywords: ["bird", "antarctic"] },
  { emoji: "🦄", name: "unicorn", keywords: ["magic", "fantasy"] },
  { emoji: "🐝", name: "bee", keywords: ["honey", "buzz"] },
  { emoji: "🦋", name: "butterfly", keywords: ["insect", "pretty"] },
  { emoji: "🐢", name: "turtle", keywords: ["slow", "tortoise"] },
  { emoji: "🐙", name: "octopus", keywords: ["squid", "tentacles"] },
  { emoji: "🦀", name: "crab", keywords: ["seafood"] },
  { emoji: "🐬", name: "dolphin", keywords: ["sea", "smart"] },
  { emoji: "🦈", name: "shark", keywords: ["jaws", "sea"] },
  // Symbols
  { emoji: "❤️", name: "red heart", keywords: ["love", "like"] },
  { emoji: "🧡", name: "orange heart", keywords: ["love"] },
  { emoji: "💛", name: "yellow heart", keywords: ["love"] },
  { emoji: "💚", name: "green heart", keywords: ["love"] },
  { emoji: "💙", name: "blue heart", keywords: ["love"] },
  { emoji: "💜", name: "purple heart", keywords: ["love"] },
  { emoji: "🖤", name: "black heart", keywords: ["love", "dark"] },
  { emoji: "🤍", name: "white heart", keywords: ["love", "pure"] },
  { emoji: "💔", name: "broken heart", keywords: ["breakup", "sad"] },
  { emoji: "❗", name: "exclamation", keywords: ["alert", "important", "!"] },
  { emoji: "❓", name: "question", keywords: ["help", "confused", "?"] },
  { emoji: "✅", name: "check", keywords: ["done", "yes", "tick", "complete"] },
  { emoji: "❌", name: "cross", keywords: ["no", "wrong", "x", "delete"] },
  { emoji: "➕", name: "plus", keywords: ["add", "more", "+"] },
  { emoji: "➖", name: "minus", keywords: ["subtract", "less", "-"] },
  { emoji: "©️", name: "copyright", keywords: ["c", "legal"] },
  { emoji: "®️", name: "registered", keywords: ["r", "legal", "trademark"] },
];
