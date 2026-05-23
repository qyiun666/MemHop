"""MeowAgentDriver — Python driver for BrainLoop cognitive loop.

Wraps the BrainLoop Rust state machine into a simple driver with
non-streaming and streaming message handling.
"""

from typing import Callable, Optional

import memhop


class MeowAgentDriver:
    """High-level driver for the MeowHop cognitive brain.

    Usage::

        driver = MeowAgentDriver(
            llm_endpoint="https://api.openai.com/v1/chat/completions",
            api_key="sk-...",
            model="gpt-4o",
            fast_model="gpt-4o-mini",
        )

        # Non-streaming
        response = driver.handle_message("Hello!")
        print(response)

        # Streaming
        def on_chunk(chunk: str):
            print(chunk, end="", flush=True)
        response = driver.handle_message_streaming("Tell me a story", on_chunk)
    """

    def __init__(
        self,
        llm_endpoint: str = "https://api.openai.com/v1/chat/completions",
        api_key: str = "",
        model: str = "gpt-4o",
        fast_model: str = "gpt-4o-mini",
        config: Optional[memhop.BrainConfig] = None,
    ):
        thinker = memhop.HttpThinker(
            endpoint=llm_endpoint,
            api_key=api_key,
            model=model,
            fast_model=fast_model,
        )
        cerebellum = memhop.FastReflex()
        self.brain = memhop.BrainLoop(
            thinker=thinker,
            cerebellum=cerebellum,
            config=config or memhop.BrainConfig(),
        )

    def handle_message(self, user_input: str) -> str:
        """Process a user message (non-streaming).

        Returns the final response after the cognitive loop completes.
        If the brain requests body actions (tools, clarification), they
        are handled automatically.
        """
        action = self.brain.process(user_input)
        while action.action_type == "NeedBody":
            # Execute body actions in a loop until Done
            body_results = self._execute_body_actions(action.actions)
            action = self.brain.feed_body_result(body_results)
        return action.for_user or ""

    def handle_message_streaming(
        self,
        user_input: str,
        on_chunk: Callable[[str], None],
    ) -> str:
        """Process a user message with streaming LLM output.

        Args:
            user_input: The user's message.
            on_chunk: Callable receiving each token as it arrives.

        Returns:
            The complete final response.
        """
        action = self.brain.process_streaming(user_input, on_chunk)
        while action.action_type == "NeedBody":
            body_results = self._execute_body_actions(action.actions)
            action = self.brain.feed_body_result(body_results)
        return action.for_user or ""

    def _execute_body_actions(self, actions) -> list[memhop.BodyResult]:
        """Execute body actions and return results.

        For now, this is a stub that returns empty results.
        Tool execution and user interaction will be wired in later versions.
        """
        results: list[memhop.BodyResult] = []
        if actions is None:
            return results
        for action in actions:
            if action.action_type == "HearMore":
                # Stub: return empty HearMore result
                results.append(memhop.BodyResult(
                    source="body",
                    text="",
                    meta={},
                ))
            elif action.action_type == "AskUser":
                # Stub: auto-confirm AskUser
                results.append(memhop.BodyResult(
                    source="body",
                    text="Proceed with caution — guidelines remain active.",
                    meta={},
                ))
            elif action.action_type == "Tool":
                # Stub: tool execution deferred
                results.append(memhop.BodyResult(
                    source=f"tool_{action.name or 'unknown'}",
                    text="",
                    meta={},
                ))
            else:
                results.append(memhop.BodyResult(
                    source="body",
                    text="",
                    meta={},
                ))
        return results
