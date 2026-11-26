"""
Natural language intent parser for Vinci TUI.

Parses user input and detects intent (create, show, edit, etc.)
Extracts entities like idea IDs, metrics, and content.
"""

import re
from typing import Optional, Dict, Any, List
from dataclasses import dataclass


@dataclass
class ParsedIntent:
    """Result of parsing user input."""
    action: str  # e.g., "create_idea", "show_notes", "recall"
    intent_type: str  # e.g., "seed", "prepare", "incubate"
    entities: Dict[str, Any]  # Extracted entities
    confidence: float  # 0.0 to 1.0
    suggestions: List[str]  # Suggested follow-up actions
    
    def to_command(self) -> Optional[List[str]]:
        """Convert parsed intent to CLI command arguments."""
        if self.action == "create_idea":
            return ["idea"] + [self.entities.get("title", "")]
        elif self.action == "set_for":
            return ["for"] + [self.entities.get("id", "")] + [self.entities.get("who", "")]
        elif self.action == "set_goal":
            return ["goal"] + [self.entities.get("id", "")]
        elif self.action == "advance_idea":
            return ["advance"] + [self.entities.get("id", "")]
        elif self.action == "recall":
            return ["recall"] + [self.entities.get("query", "")]
        elif self.action == "remember":
            return ["remember"] + [self.entities.get("content", "")]
        elif self.action == "show_idea":
            return ["show"] + [self.entities.get("id", "")]
        return None


class IntentParser:
    """Parses natural language into structured intent."""
    
    # Patterns for detecting actions
    PATTERNS = {
        "create_idea": [
            r"create (a|an) new (project|idea|task) to? (.+)",
            r"new (project|idea|task): (.+)",
            r"(add|start) (.+)",
        ],
        "set_for": [
            r"(who is this for|for whom) (.+)",
        ],
        "set_goal": [
            r"set goal to (.+)",
            r"goal: (.+)",
            r"measure (.+) by (.+)",
        ],
        "advance_idea": [
            r"(move|advance|progress) (.+) to? ((seed|prepare|incubate|build|tend|retired))",
            r"advance (.+) stage",
        ],
        "recall": [
            r"show me (.+)",
            r"recall (.+)",
            r"what did I say about (.+)",
            r"remember when I (.+)",
            r"find notes about (.+)",
        ],
        "remember": [
            r"(I remember|note down) that (.+)",
            r"fact: (.+)",
        ],
        "show_idea": [
            r"show (.+) details",
            r"view (.+) status",
        ],
    }
    
    # Intent type patterns
    INTENT_PATTERNS = {
        "seed": [
            r"(new|first) (project|idea)",
            r"(just|initially) started",
        ],
        "prepare": [
            r"goal to (.+)",
            r"define scope for (.+)",
            r"what is it for (.+)",
        ],
        "incubate": [
            r"gathering (info|notes|requirements)",
            r"researching (.+)",
            r"learning about (.+)",
        ],
        "build": [
            r"(building|implementing|working on) (.+)",
            r"in progress",
            r"active work",
        ],
        "tend": [
            r"shipped",
            r"completed",
            r"finished",
        ],
    }
    
    # Metric patterns
    METRIC_PATTERNS = {
        r"(at least|minimum) (.+):? (.+)",
        r"(at most|maximum) (.+):? (.+)",
        r"(between|range) (.+) to? (.+)",
        r"target: (.+)",
        r"goal is (.+)",
    }
    
    def __init__(self, verbose: bool = False):
        self.verbose = verbose
    
    def parse(self, text: str) -> ParsedIntent:
        """Parse natural language text into structured intent."""
        text_lower = text.lower().strip()
        
        # Detect action
        action, confidence = self._detect_action(text_lower)
        
        # Extract entities based on action
        entities = self._extract_entities(text_lower, action)
        
        # Determine intent type
        intent_type = self._detect_intent_type(text_lower, entities)
        
        # Generate suggestions
        suggestions = self._generate_suggestions(text_lower, action, entities)
        
        return ParsedIntent(
            action=action,
            intent_type=intent_type,
            entities=entities,
            confidence=confidence,
            suggestions=suggestions,
        )
    
    def _detect_action(self, text: str) -> tuple[str, float]:
        """Detect the user's intended action."""
        # Try pattern matching first
        for action, patterns in self.PATTERNS.items():
            for pattern in patterns:
                if re.search(pattern, text):
                    return action, 0.85
        
        # Check for common commands directly
        direct_commands = {
            "idea": r"^(vinci\s+)?idea\b",
            "recall": r"^(vinci\s+)?recall\b",
            "show": r"^(vinci\s+)?show\b",
            "pipeline": r"^(vinci\s+)?pipeline\b",
        }
        
        for cmd, pattern in direct_commands.items():
            if re.match(pattern, text):
                return cmd, 0.95
        
        # Default to recall/search if nothing else matches
        if len(text.split()) > 1:
            return "recall", 0.6
        
        return "recall", 0.5
    
    def _extract_entities(self, text: str, action: str) -> Dict[str, Any]:
        """Extract relevant entities from the parsed text."""
        entities = {}
        
        if action == "create_idea":
            # Extract title/project description
            matches = re.search(r"(?:project|idea|task):?\s*(.+)", text)
            if not matches:
                matches = re.search(r"to? (.+)$", text)
            if matches:
                entities["title"] = matches.group(1).strip()
        
        elif action == "set_for":
            # Extract who it's for
            match = re.search(r"for\s+(.+)", text)
            if match:
                entities["who"] = match.group(1).strip()
        
        elif action == "set_goal":
            # Extract goal description
            matches = re.search(r"goal to? (.+)", text)
            if not matches:
                matches = re.search(r"(?:measure|target):?\s*(.+)$", text)
            if matches:
                entities["goal"] = matches.group(1).strip()
            
            # Try to extract metric
            metric_match = re.search(r"(\d+)\s*(times?|minutes?|hours?|days?)?", text)
            if metric_match:
                entities["metric_value"] = int(metric_match.group(1))
                entities["metric_unit"] = metric_match.group(2) or ""
        
        elif action == "advance_idea":
            # Extract idea ID and target stage
            match = re.search(r"(?:move|advance)\s+(\w+)\s+to?\s+(.+)", text)
            if match:
                entities["id"] = match.group(1)
                entities["stage"] = match.group(2).lower()
        
        elif action == "recall":
            # Extract query terms
            matches = re.search(r"(?:show|find|recall)\s+(.+)$", text)
            if matches:
                entities["query"] = matches.group(1).strip()
        
        elif action == "remember":
            # Extract content to remember
            matches = re.search(r"that\s+(.+)$", text)
            if not matches:
                matches = re.search(r"(?:fact|note):\s*(.+)$", text)
            if matches:
                entities["content"] = matches.group(1).strip()
        
        elif action == "show_idea":
            # Extract idea ID
            match = re.search(r"show\s+(\w+)\s+details", text)
            if match:
                entities["id"] = match.group(1)
        
        return entities
    
    def _detect_intent_type(self, text: str, entities: Dict[str, Any]) -> str:
        """Determine the pipeline stage/intent type."""
        # Check intent patterns first
        for intent_type, patterns in self.INTENT_PATTERNS.items():
            for pattern in patterns:
                if re.search(pattern, text):
                    return intent_type
        
        # Default based on action
        if entities.get("stage"):
            return entities["stage"]
        
        return "seed"  # Default to seed for new ideas
    
    def _generate_suggestions(self, text: str, action: str, entities: Dict[str, Any]) -> List[str]:
        """Generate follow-up suggestions based on parsed intent."""
        suggestions = []
        
        if action == "create_idea":
            suggestions.append("Set a goal for this idea")
            suggestions.append("Define who it's for")
        
        elif action == "set_goal":
            suggestions.append("Advance to Prepare stage")
            suggestions.append("Add requirements")
        
        elif action == "advance_idea":
            if entities.get("stage") == "build":
                suggestions.append("Set this week's commitment")
                suggestions.append("Create a working directory")
        
        elif action == "recall":
            suggestions.append("View related ideas")
            suggestions.append("Show timeline of events")
        
        return suggestions[:3]  # Limit to 3 suggestions
