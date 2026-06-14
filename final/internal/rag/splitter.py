from dataclasses import dataclass
from typing import List, Optional


@dataclass
class Chunk:
    id: int
    content: str


class RecursiveSplitter:
    """按固定窗口递归切分文本，保留 overlap 上下文。"""

    def __init__(self, chunk_size: int = 200, overlap: int = 50, separators: Optional[List[str]] = None):
        self.chunk_size = max(1, int(chunk_size or 1))
        self.overlap = max(0, min(int(overlap or 0), self.chunk_size - 1))
        self.separators = separators or ["\n\n", "\n", "。", "；", "，", " "]

    def split(self, text: str) -> List[Chunk]:
        if not text:
            return []
        step = self.chunk_size - self.overlap
        chunks: List[Chunk] = []
        idx = 0
        start = 0
        while start < len(text):
            end = min(start + self.chunk_size, len(text))
            chunks.append(Chunk(id=idx, content=text[start:end]))
            idx += 1
            if end >= len(text):
                break
            start += step
        return chunks
