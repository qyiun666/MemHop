"""DeepSeek API client for LLM-as-judge evaluation.

Usage:
    export DEEPSEEK_API_KEY="sk-..."
    judge = DeepSeekJudge()
    result = judge.evaluate(question, context, expected_answer)
"""

import json
import os
import urllib.request
import urllib.error
from typing import Optional

DEFAULT_MODEL = "deepseek-chat"
API_URL = "https://api.deepseek.com/v1/chat/completions"


class DeepSeekJudge:
    """Lightweight DeepSeek API client for evaluation."""

    def __init__(self, model: str = DEFAULT_MODEL):
        self.api_key = os.environ.get("DEEPSEEK_API_KEY", "")
        if not self.api_key:
            raise RuntimeError(
                "DEEPSEEK_API_KEY not set. "
                "Export it: export DEEPSEEK_API_KEY='sk-...'"
            )
        self.model = model

    def ask(self, messages: list[dict], temperature: float = 0.0) -> str:
        """Send a chat completion request to DeepSeek.

        Args:
            messages: OpenAI-format messages list.
            temperature: Sampling temperature (0 = deterministic).

        Returns:
            Response content string.
        """
        body = json.dumps({
            "model": self.model,
            "messages": messages,
            "temperature": temperature,
            "max_tokens": 512,
        }).encode()

        req = urllib.request.Request(
            API_URL,
            data=body,
            headers={
                "Content-Type": "application/json",
                "Authorization": f"Bearer {self.api_key}",
            },
            method="POST",
        )

        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                data = json.loads(resp.read())
            choices = data.get("choices", [])
            if not choices:
                return ""
            return choices[0].get("message", {}).get("content", "")
        except urllib.error.HTTPError as e:
            raise RuntimeError(f"DeepSeek API error {e.code}: {e.read().decode()}")

    def generate_dmr_questions(
        self, sessions_text: str, personas: list[str], n_questions: int = 5
    ) -> list[dict]:
        """Generate DMR-style Q&A pairs from conversation content.

        Args:
            sessions_text: Text of sessions 1-4 (the stored content).
            personas: Speaker persona descriptions.
            n_questions: Number of questions to generate.

        Returns:
            list of {"question": "...", "answer": "..."}
        """
        prompt = (
            "You are generating a Deep Memory Retrieval test.\n\n"
            f"Below are {n_questions-1} chat sessions between two speakers "
            f"with the following personas:\n"
            f"Speaker 1: {personas[0] if personas else 'Unknown'}\n"
            f"Speaker 2: {personas[1] if len(personas) > 1 else 'Unknown'}\n\n"
            f"Conversation text:\n{sessions_text}\n\n"
            "Generate EXACTLY 5 question-answer pairs about facts mentioned "
            "in the EARLY sessions (not the last one). "
            "Questions should test memory of specific details: names, hobbies, "
            "plans, preferences, locations, etc.\n\n"
            "Return ONLY valid JSON array: "
            '[{"question": "...", "answer": "..."}, ...]\n'
            "No markdown, no explanation."
        )

        response = self.ask([
            {"role": "system", "content": "You are a test data generator. Output only valid JSON."},
            {"role": "user", "content": prompt},
        ])

        # Parse JSON from response
        try:
            qa_pairs = json.loads(response)
        except json.JSONDecodeError:
            # Try to extract JSON from markdown
            import re
            match = re.search(r'\[.*?\]', response, re.DOTALL)
            if match:
                qa_pairs = json.loads(match.group())
            else:
                raise RuntimeError(f"Failed to parse LLM response: {response[:200]}")

        return qa_pairs[:n_questions]

    def evaluate_answer(
        self, question: str, expected_answer: str, context: str
    ) -> float:
        """Evaluate if the context contains enough info to answer the question.

        Returns 1.0 if the answer can be derived from context, 0.0 otherwise.
        """
        prompt = (
            "You are evaluating whether a piece of context contains "
            "the information needed to answer a question.\n\n"
            f"Question: {question}\n"
            f"Expected answer: {expected_answer}\n"
            f"Context:\n{context}\n\n"
            "Does the context contain enough information to produce "
            "the expected answer? Reply ONLY with 'YES' or 'NO'."
        )

        response = self.ask([
            {"role": "system", "content": "Reply only YES or NO."},
            {"role": "user", "content": prompt},
        ])

        return 1.0 if response.strip().upper().startswith("YES") else 0.0
