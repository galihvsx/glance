// PublicIntakeForm (C82T1): the public, unauthenticated submission page
// at `/f/:token`. No app shell, no login prompts — the token is the only
// capability. Unknown/disabled tokens 404 and read as
// "This form is unavailable." with no manage UI leakage. Mirrors the
// PublicShare page pattern. The form fields render through the shared
// FormRenderer — the same component the builder's live preview uses.

import { useState, type FormEvent } from "react";
import { useParams } from "react-router-dom";
import { useMutation, useQuery } from "@tanstack/react-query";
import { CheckCircle2 } from "lucide-react";
import { ApiError } from "../lib/api";
import {
  buildSubmitValues,
  fetchPublicIntakeForm,
  missingRequiredLabels,
  publicIntakeFormKey,
  submitPublicIntakeForm,
} from "../lib/intakeForms";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Skeleton } from "../components/ui/skeleton";
import FormRenderer, { type FormRendererValues } from "../components/intake/FormRenderer";

export default function PublicIntakeForm() {
  const { token = "" } = useParams<{ token: string }>();

  const formQuery = useQuery({
    queryKey: publicIntakeFormKey(token),
    queryFn: () => fetchPublicIntakeForm(token),
    retry: false,
  });

  const [values, setValues] = useState<FormRendererValues>({});
  const [validationError, setValidationError] = useState<string | null>(null);
  const [submitted, setSubmitted] = useState(false);

  const submitMutation = useMutation({
    mutationFn: () => {
      const fields = formQuery.data?.fields ?? [];
      return submitPublicIntakeForm(token, buildSubmitValues(fields, values));
    },
    onSuccess: () => {
      setValidationError(null);
      setSubmitted(true);
    },
    onError: (e) => {
      setValidationError(
        e instanceof ApiError ? e.message : "Submission failed — please try again.",
      );
    },
  });

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    const fields = formQuery.data?.fields ?? [];
    const missing = missingRequiredLabels(fields, values);
    if (missing.length > 0) {
      setValidationError(
        `Please fill in the required fields: ${missing.join(", ")}.`,
      );
      return;
    }
    setValidationError(null);
    submitMutation.mutate();
  }

  function reset() {
    setValues({});
    setValidationError(null);
    setSubmitted(false);
  }

  return (
    <div className="mx-auto max-w-2xl px-4 py-10">
      <header className="mb-6">
        <span className="text-lg font-bold">glance</span>
      </header>

      {formQuery.isLoading ? (
        <div className="space-y-3">
          <Skeleton className="h-8 w-2/3" />
          <Skeleton className="h-4 w-full" />
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-[68px] w-full" />
          ))}
        </div>
      ) : formQuery.isError || !formQuery.data ? (
        <Card>
          <CardHeader>
            <CardTitle>This form is unavailable.</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-sm text-muted-foreground">
              This link is invalid, was disabled, or the form was deleted.
            </p>
          </CardContent>
        </Card>
      ) : submitted ? (
        <Card>
          <CardContent className="flex flex-col items-center gap-3 py-10 text-center">
            <CheckCircle2
              className="h-10 w-10 text-accent-green"
              aria-hidden
            />
            <p className="text-lg font-medium">Response submitted</p>
            <p className="max-w-sm text-sm text-muted-foreground">
              Response submitted — the team will review it in Intake.
            </p>
            <Button variant="outline" onClick={reset}>
              Submit another response
            </Button>
          </CardContent>
        </Card>
      ) : (
        <>
          <div className="mb-6">
            <h1 className="text-2xl font-bold">{formQuery.data.name}</h1>
            {formQuery.data.description && (
              <p className="mt-1 text-sm text-muted-foreground">
                {formQuery.data.description}
              </p>
            )}
            <p className="mt-1 text-xs text-muted-foreground">
              {formQuery.data.project_name} · Intake
            </p>
          </div>

          <form onSubmit={handleSubmit}>
            <FormRenderer
              fields={formQuery.data.fields}
              values={values}
              onChange={(key, value) =>
                setValues((prev) => ({ ...prev, [key]: value }))
              }
              disabled={submitMutation.isPending}
            />

            {validationError && (
              <Alert variant="destructive" className="mt-4">
                <AlertDescription>{validationError}</AlertDescription>
              </Alert>
            )}

            <div className="mt-6">
              <Button type="submit" disabled={submitMutation.isPending}>
                {submitMutation.isPending ? "Submitting…" : "Submit"}
              </Button>
            </div>
          </form>
        </>
      )}

      <footer className="mt-10 text-center text-xs text-muted-foreground">
        Shared from glance.
      </footer>
    </div>
  );
}
